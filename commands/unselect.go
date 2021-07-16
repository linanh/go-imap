package commands

import (
	"github.com/linanh/go-imap"
)

// Unselect is a UNSELECT command, as defined in RFC 3691
type Unselect struct{}

func (cmd *Unselect) Command() *imap.Command {
	return &imap.Command{
		Name: "UNSELECT",
	}
}

func (cmd *Unselect) Parse(fields []interface{}) error {
	return nil
}
