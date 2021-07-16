package commands

import (
	"errors"
	"strings"

	"github.com/linanh/go-imap"
	"github.com/linanh/go-imap/utf7"
)

// Select is a SELECT command, as defined in RFC 3501 section 6.3.1. If ReadOnly
// is set to true, the EXAMINE command will be used instead.
type Select struct {
	Mailbox         string
	QresyncParams   []string
	EnableCondstore bool
	ReadOnly        bool
}

func (cmd *Select) Command() *imap.Command {
	name := "SELECT"
	if cmd.ReadOnly {
		name = "EXAMINE"
	}

	mailbox, _ := utf7.Encoding.NewEncoder().String(cmd.Mailbox)

	return &imap.Command{
		Name:      name,
		Arguments: []interface{}{imap.FormatMailboxName(mailbox)},
	}
}

func (cmd *Select) Parse(fields []interface{}) error {
	if len(fields) < 1 {
		return errors.New("No enough arguments")
	}

	if mailbox, err := imap.ParseString(fields[0]); err != nil {
		return err
	} else if mailbox, err := utf7.Encoding.NewDecoder().String(mailbox); err != nil {
		return err
	} else {
		cmd.Mailbox = imap.CanonicalMailboxName(mailbox)
	}

	//RFC 7162
	if len(fields) > 1 {
		switch args := fields[1].(type) {
		case []interface{}:
			if len(args) <= 1 {
				return nil
			}
			argStr, _ := args[0].(string)
			//RFC 7162 3.1.8 CONDSTORE Parameter to SELECT and EXAMINE
			if strings.ToUpper(argStr) == "CONDSTORE" {
				cmd.EnableCondstore = true
				return nil
			} else if strings.ToUpper(argStr) != "QRESYNC" {
				return nil
			}
			//RFC 7162 3.1.8 CONDSTORE Parameter to SELECT and EXAMINE
			switch qresyncArgs := args[1].(type) {
			case []interface{}:
				alen := len(qresyncArgs)
				if alen < 4 {
					cmd.QresyncParams = make([]string, alen)
				} else {
					cmd.QresyncParams = make([]string, 5)
				}
				if alen >= 2 {
					cmd.QresyncParams[0], _ = imap.ParseString(qresyncArgs[0])
					cmd.QresyncParams[1], _ = imap.ParseString(qresyncArgs[1])
				}
				if alen >= 3 {
					cmd.QresyncParams[2], _ = imap.ParseString(qresyncArgs[2])
				}
				if alen >= 4 {
					switch arg4th := qresyncArgs[3].(type) {
					case []interface{}:
						if len(arg4th) != 2 {
							return errors.New("No enough arguments")
						}
						cmd.QresyncParams[3], _ = imap.ParseString(arg4th[0])
						cmd.QresyncParams[4], _ = imap.ParseString(arg4th[1])
					}
				}
			}
		}
	}

	return nil
}
