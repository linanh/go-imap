package backend

import (
	"strconv"

	"github.com/linanh/go-imap"
)

type QresyncSelect struct {
	UIDValidity uint32
	LastModseq  uint64
	UIDSet      *imap.SeqSet
	SeqSetPair  *imap.SeqSet
	UIDSetPair  *imap.SeqSet
}

func (QresyncSelect) ExtOption() {}

type QresyncFetch struct {
	EnableVanished bool
}

func (QresyncFetch) ExtOption() {}

type QresyncVanished struct {
	SeqSet  *imap.SeqSet
	Earlier bool
}

func (qv *QresyncVanished) ExtResult() {}
func (qv *QresyncVanished) WriteTo(w *imap.Writer) error {
	respData := []interface{}{
		imap.RawString("VANISHED"),
	}
	if qv.Earlier {
		respData = append(respData, []interface{}{imap.RawString("EARLIER")})
	}
	respData = append(respData, imap.RawString(qv.SeqSet.String()))

	return imap.NewUntaggedResp(respData).WriteTo(w)

}

type QresyncMessage struct {
	SeqNum uint32
	Flags  []string
	UID    uint32
	Modseq uint64
}
type QresyncMessages struct {
	Data []*QresyncMessage
}

func (qm *QresyncMessages) ExtResult() {}
func (qm *QresyncMessages) WriteTo(w *imap.Writer) error {
	var err error
	for _, msg := range qm.Data {
		flags := make([]interface{}, len(msg.Flags))
		for i, f := range msg.Flags {
			flags[i] = imap.RawString(f)
		}
		resp := imap.NewUntaggedResp([]interface{}{
			imap.RawString(strconv.Itoa(int(msg.SeqNum))),
			imap.RawString("FETCH"),
			[]interface{}{
				imap.RawString("UID"),
				imap.RawString(strconv.Itoa(int(msg.UID))),
				imap.RawString("MODSEQ"),
				[]interface{}{strconv.Itoa(int(msg.Modseq))},
				imap.RawString("FLAGS"),
				flags,
			},
		})
		err = resp.WriteTo(w)
	}
	return err
}
