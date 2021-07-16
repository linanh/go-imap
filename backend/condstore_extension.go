package backend

import (
	"strconv"

	"github.com/linanh/go-imap"
)

type CondstoreSelect struct {
	Condstore bool
}

func (CondstoreSelect) ExtOption() {}

type CondstoreStore struct {
	UnchangedSince uint64
}

func (CondstoreStore) ExtOption() {}

type CondstoreFetch struct {
	ChangeSinced uint64
}

func (CondstoreFetch) ExtOption() {}

type HighestModseqResp struct {
	HighestModseqResp uint64
}

func (hm *HighestModseqResp) ExtResult() {}

func (hm *HighestModseqResp) WriteTo(w *imap.Writer) error {

	resp := &imap.StatusResp{
		Tag:       "*",
		Type:      imap.StatusRespOk,
		Code:      imap.CodeHighestModseq,
		Arguments: []interface{}{strconv.Itoa(int(hm.HighestModseqResp))},
		Info:      "",
	}
	return resp.WriteTo(w)

}

type SearchModseqResp struct {
	Modseq uint64
}

func (hm *SearchModseqResp) ExtResult() {}
