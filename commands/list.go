package commands

import (
	"errors"
	"strings"

	"github.com/linanh/go-imap"
	"github.com/linanh/go-imap/utf7"
)

// List is a LIST command, as defined in RFC 3501 section 6.3.8. If Subscribed
// is set to true, LSUB will be used instead.
type List struct {
	Reference  string
	Mailbox    string
	Return     map[string][]interface{}
	Subscribed bool
}

func (cmd *List) Command() *imap.Command {
	name := "LIST"
	if cmd.Subscribed {
		name = "LSUB"
	}

	enc := utf7.Encoding.NewEncoder()
	ref, _ := enc.String(cmd.Reference)
	mailbox, _ := enc.String(cmd.Mailbox)

	return &imap.Command{
		Name:      name,
		Arguments: []interface{}{ref, mailbox},
	}
}

func (cmd *List) Parse(fields []interface{}) error {
	if len(fields) < 2 {
		return errors.New("No enough arguments")
	}

	dec := utf7.Encoding.NewDecoder()

	if mailbox, err := imap.ParseString(fields[0]); err != nil {
		return err
	} else if mailbox, err := dec.String(mailbox); err != nil {
		return err
	} else {
		// TODO: canonical mailbox path
		cmd.Reference = imap.CanonicalMailboxName(mailbox)
	}

	if mailbox, err := imap.ParseString(fields[1]); err != nil {
		return err
	} else if mailbox, err := dec.String(mailbox); err != nil {
		return err
	} else {
		cmd.Mailbox = imap.CanonicalMailboxName(mailbox)
	}

	//example: list "" "*" return (status (x-guid) children)
	if len(fields) > 3 {
		cmd.Return = map[string][]interface{}{}
		returnStr, _ := imap.ParseString(fields[2])
		if strings.ToUpper(returnStr) == "RETURN" {
			switch args := fields[3].(type) {
			case []interface{}:
				for i := 0; i < len(args); i++ {
					switch arg := args[i].(type) {
					case string, imap.RawString:
						argStr, _ := imap.ParseString(arg)
						i++
						if i < len(args) {
							if arg, ok := args[i].([]interface{}); ok {
								cmd.Return[strings.ToUpper(argStr)] = arg
								continue
							}
						}
						cmd.Return[strings.ToUpper(argStr)] = nil
					}
				}
			}
		}
	}

	return nil
}
