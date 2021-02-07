//file from https://github.com/moby/moby/blob/2445e6b99d4beecb25d556d9a099bdf47703e174/daemon/logger/jsonfilelog/multireader/multireader.go
//add startOffset NewCombinedBuf
package imap

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"io/ioutil"
)

type pos struct {
	idx    int
	offset int64
}

type cleanupFunc func() error

type MultiReadSeeker interface {
	io.Seeker
	io.Reader
	io.Closer
	Len() int
	Size() int64
}

type multiReadSeeker struct {
	readers 	[]io.ReadSeeker
	pos     	*pos
	posIdx  	map[io.ReadSeeker]int
	startOffset []int64
	cleanup 	cleanupFunc
	size		int64			
}

func (r *multiReadSeeker) Close() error {
	if r.cleanup != nil {
		return r.cleanup()
	}
	return nil
}

func (r *multiReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var tmpOffset int64
	switch whence {
	case os.SEEK_SET:
		for i, rdr := range r.readers {
			// get size of the current reader
			s, err := rdr.Seek(0-r.startOffset[i], os.SEEK_END)
			if err != nil {
				return -1, err
			}

			if offset > tmpOffset+s {
				if i == len(r.readers)-1 {
					rdrOffset := s + (offset - tmpOffset)
					if _, err := rdr.Seek(rdrOffset+r.startOffset[i], os.SEEK_SET); err != nil {
						return -1, err
					}
					r.pos = &pos{i, rdrOffset}
					return offset, nil
				}

				tmpOffset += s
				continue
			}

			rdrOffset := offset - tmpOffset
			idx := i

			rdr.Seek(rdrOffset+r.startOffset[i], os.SEEK_SET)
			// make sure all following readers are at 0
			for j, rdr := range r.readers[i+1:] {
				rdr.Seek(r.startOffset[j+i+1], os.SEEK_SET)
			}

			if rdrOffset == s && i != len(r.readers)-1 {
				idx++
				rdrOffset = 0
			}
			r.pos = &pos{idx, rdrOffset}
			return offset, nil
		}
	case os.SEEK_END:
		for i, rdr := range r.readers {
			s, err := rdr.Seek(0-r.startOffset[i], os.SEEK_END)
			if err != nil {
				return -1, err
			}
			tmpOffset += s
		}
		r.Seek(tmpOffset+offset, os.SEEK_SET)
		return tmpOffset + offset, nil
	case os.SEEK_CUR:
		if r.pos == nil {
			return r.Seek(offset, os.SEEK_SET)
		}
		// Just return the current offset
		if offset == 0 {
			return r.getCurOffset()
		}

		curOffset, err := r.getCurOffset()
		if err != nil {
			return -1, err
		}
		rdr, rdrOffset, err := r.getReaderForOffset(curOffset + offset)
		if err != nil {
			return -1, err
		}

		r.pos = &pos{r.posIdx[rdr], rdrOffset}
		return curOffset + offset, nil
	default:
		return -1, fmt.Errorf("Invalid whence: %d", whence)
	}

	return -1, fmt.Errorf("Error seeking for whence: %d, offset: %d", whence, offset)
}

func (r *multiReadSeeker) getReaderForOffset(offset int64) (io.ReadSeeker, int64, error) {

	var offsetTo int64

	for i, rdr := range r.readers {
		size, err := getReadSeekerSize(rdr, r.startOffset[i])
		if err != nil {
			return nil, -1, err
		}
		if offsetTo+size > offset {
			return rdr, offset - offsetTo, nil
		}
		if rdr == r.readers[len(r.readers)-1] {
			return rdr, offsetTo + offset, nil
		}
		offsetTo += size
	}

	return nil, 0, nil
}

func (r *multiReadSeeker) getCurOffset() (int64, error) {
	var totalSize int64
	for i, rdr := range r.readers[:r.pos.idx+1] {
		if r.posIdx[rdr] == r.pos.idx {
			totalSize += r.pos.offset
			break
		}

		size, err := getReadSeekerSize(rdr, r.startOffset[i])
		if err != nil {
			return -1, fmt.Errorf("error getting seeker size: %v", err)
		}
		totalSize += size
	}
	return totalSize, nil
}

func (r *multiReadSeeker) getOffsetToReader(rdr io.ReadSeeker) (int64, error) {
	var offset int64
	for i, r2 := range r.readers {
		if r2 == rdr {
			break
		}

		size, err := getReadSeekerSize(rdr, r.startOffset[i])
		if err != nil {
			return -1, err
		}
		offset += size
	}
	return offset, nil
}

func (r *multiReadSeeker) Read(b []byte) (int, error) {
	if r.pos == nil {
		// make sure all readers are at 0
		r.Seek(0, os.SEEK_SET)
	}

	bLen := int64(len(b))
	buf := bytes.NewBuffer(nil)
	var rdr io.ReadSeeker

	for _, rdr = range r.readers[r.pos.idx:] {
		readBytes, err := io.CopyN(buf, rdr, bLen)
		if err != nil && err != io.EOF {
			return -1, err
		}
		bLen -= readBytes

		if bLen == 0 {
			break
		}
	}

	rdrPos, err := rdr.Seek(0, os.SEEK_CUR)
	if err != nil {
		return -1, err
	}
	r.pos = &pos{r.posIdx[rdr], rdrPos}
	return buf.Read(b)
}

func (r *multiReadSeeker) Size() int64 {
	if r.size > 0 {
		return r.size
	}
	curPos, _ := r.Seek(0, os.SEEK_CUR)
	l, _ := r.Seek(0, os.SEEK_END)
	r.Seek(curPos, os.SEEK_SET)
	return l
}

func (r *multiReadSeeker) Len() int {
	return int(r.Size())
}

func getReadSeekerSize(rdr io.ReadSeeker, startOffset int64) (int64, error) {

	// save the current position
	pos, err := rdr.Seek(0, os.SEEK_CUR)
	if err != nil {
		return -1, err
	}

	// get the size
	size, err := rdr.Seek(0-startOffset, os.SEEK_END)
	if err != nil {
		return -1, err
	}

	// reset the position
	if _, err := rdr.Seek(pos+startOffset, os.SEEK_SET); err != nil {
		return -1, err
	}
	return size, nil
}

// MultiReadSeeker returns a ReadSeeker that's the logical concatenation of the provided
// input readseekers. After calling this method the initial position is set to the
// beginning of the first ReadSeeker. At the end of a ReadSeeker, Read always advances
// to the beginning of the next ReadSeeker and returns EOF at the end of the last ReadSeeker.
// Seek can be used over the sum of lengths of all readseekers.
//
// When a MultiReadSeeker is used, no Read and Seek operations should be made on
// its ReadSeeker components. Also, users should make no assumption on the state
// of individual readseekers while the MultiReadSeeker is used.
func NewMultiReadSeeker(startOffset []int64, readers ...io.ReadSeeker) MultiReadSeeker {
	if startOffset == nil || len(startOffset) == 0 {
		startOffset = make([]int64, len(readers))
	}
	idx := make(map[io.ReadSeeker]int)
	for i, rdr := range readers {
		idx[rdr] = i
	}
	return &multiReadSeeker{
		readers: readers,
		posIdx:  idx,
		startOffset: startOffset,
	}
}

func NewMultiReadSeekCloser(startOffset []int64, size int64, cleanup cleanupFunc, readers ...io.ReadSeeker) MultiReadSeeker {
	if startOffset == nil || len(startOffset) == 0 {
		startOffset = make([]int64, len(readers))
	}
	idx := make(map[io.ReadSeeker]int)
	for i, rdr := range readers {
		idx[rdr] = i
	}
	return &multiReadSeeker{
		readers: readers,
		posIdx:  idx,
		startOffset: startOffset,
		size: size,
		cleanup: cleanup,
	}
}

var (
	CombinedBufMemBytes int64 = 1048576
	CombinedBufMaxBytes int64 = 1048576000
	CombinedBufTempFilePrefix string = "combined-buf-"
)

func NewCombinedBuf(input io.Reader, inputsize int64) (MultiReadSeeker, error) {
	if inputsize > CombinedBufMaxBytes {
		return nil, fmt.Errorf("message size too big.")
	}
	memBytes := CombinedBufMemBytes
	if memBytes > inputsize {
		memBytes = inputsize
	}
	memReader := &io.LimitedReader{
		R: input,      
		N: memBytes, 
	}

	readers := make([]io.ReadSeeker, 0, 2)
	buffer, err := ioutil.ReadAll(memReader)
	if err != nil {
		return nil, err
	}

	readers = append(readers, bytes.NewReader(buffer))

	var file *os.File

	totalBytes := int64(len(buffer))
	if inputsize > totalBytes {
		tempFilePrefix := CombinedBufTempFilePrefix
		file, err = ioutil.TempFile("", tempFilePrefix)
		if err != nil {
			return nil, err
		}

		readSrc := &io.LimitedReader{R: input, N: inputsize-totalBytes}

		writtenBytes, err := io.Copy(file, readSrc)
		if err != nil {
			return nil, err
		}
		totalBytes += writtenBytes
		file.Seek(0, 0)
		readers = append(readers, file)
	}

	if totalBytes != inputsize {
		if file != nil {
				file.Close()
				os.Remove(file.Name())
		}
		return nil, io.ErrUnexpectedEOF 
	}

	var cleanup cleanupFunc
	if file != nil {
		cleanup = func() error {
			file.Close()
			os.Remove(file.Name())
			return nil
		}
	}
	return NewMultiReadSeekCloser(nil, totalBytes, cleanup, readers...), nil
}