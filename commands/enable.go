package commands

import (
	"errors"

	"github.com/linanh/go-imap"
)

// Enable is a ENABLE command, as defined in RFC 7162 section 3.2.3.
type Enable struct {
	Qresync   string
	Condstore string
}

func (cmd *Enable) Command() *imap.Command {
	return &imap.Command{
		Name:      "ENABLE",
		Arguments: []interface{}{cmd.Qresync, cmd.Condstore},
	}
}

func (cmd *Enable) Parse(fields []interface{}) error {
	if len(fields) < 1 {
		return errors.New("Not enough arguments")
	}

	var err error
	if cmd.Qresync, err = imap.ParseString(fields[0]); err != nil {
		return err
	}
	if len(fields) > 1 {
		if cmd.Condstore, err = imap.ParseString(fields[1]); err != nil {
			return err
		}
	}

	return nil
}
