package commands

import (
	"errors"
	"strings"

	"github.com/linanh/go-imap"
)

// Fetch is a FETCH command, as defined in RFC 3501 section 6.4.5.
type Fetch struct {
	SeqSet         *imap.SeqSet
	Items          []imap.FetchItem
	ChangeSinced   uint64
	EnableVanished bool
}

func (cmd *Fetch) Command() *imap.Command {
	items := make([]interface{}, len(cmd.Items))
	for i, item := range cmd.Items {
		items[i] = imap.RawString(item)
	}

	return &imap.Command{
		Name:      "FETCH",
		Arguments: []interface{}{cmd.SeqSet, items},
	}
}

func (cmd *Fetch) Parse(fields []interface{}) error {
	if len(fields) < 2 {
		return errors.New("No enough arguments")
	}

	var err error
	if seqset, ok := fields[0].(string); !ok {
		return errors.New("Sequence set must be an atom")
	} else if cmd.SeqSet, err = imap.ParseSeqSet(seqset); err != nil {
		return err
	}

	switch items := fields[1].(type) {
	case string: // A macro or a single item
		cmd.Items = imap.FetchItem(strings.ToUpper(items)).Expand()
	case []interface{}: // A list of items
		cmd.Items = make([]imap.FetchItem, 0, len(items))
		for _, v := range items {
			itemStr, _ := v.(string)
			item := imap.FetchItem(strings.ToUpper(itemStr))
			cmd.Items = append(cmd.Items, item.Expand()...)
		}
	default:
		return errors.New("Items must be either a string or a list")
	}

	//RFC 7162 3.1.4.1 CHANGEDSINCE FETCH Modifier
	if len(fields) > 2 {
		switch args := fields[2].(type) {
		case []interface{}:
			if len(args) > 1 {
				changedSinceKey, _ := imap.ParseString(args[0])
				if strings.ToUpper(changedSinceKey) == "CHANGEDSINCE" {
					cmd.ChangeSinced, _ = imap.ParseNumber64bit(args[1])
					//response contains MODSEQ
					cmd.Items = append(cmd.Items, imap.FetchModseq)
				}
			}
			if len(args) > 2 {
				vanishedKey, _ := imap.ParseString(args[2])
				if strings.ToUpper(vanishedKey) == "VANISHED" {
					cmd.EnableVanished = true
				}
			}
		}
	}

	return nil
}
