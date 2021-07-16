package server

import (
	"errors"
	"strings"

	"github.com/linanh/go-imap"
	"github.com/linanh/go-imap/backend"
	"github.com/linanh/go-imap/commands"
	"github.com/linanh/go-imap/responses"
)

// imap errors in Authenticated state.
var (
	ErrNotAuthenticated = errors.New("Not authenticated")
)

type Select struct {
	commands.Select
}

func (cmd *Select) Handle(conn Conn) error {
	ctx := conn.Context()

	if ctx.Mailbox != nil {
		ctx.Mailbox.DeSelect()
	}
	// As per RFC1730#6.3.1,
	// 		The SELECT command automatically deselects any
	// 		currently selected mailbox before attempting the new selection.
	// 		Consequently, if a mailbox is selected and a SELECT command that
	// 		fails is attempted, no mailbox is selected.
	// For example, some clients (e.g. Apple Mail) perform SELECT "" when the
	// server doesn't announce the UNSELECT capability.
	ctx.Mailbox = nil
	ctx.MailboxReadOnly = false

	if ctx.User == nil {
		return ErrNotAuthenticated
	}
	mbox, err := ctx.User.GetMailbox(cmd.Mailbox)
	if err != nil {
		return err
	}

	var extOpts []backend.ExtensionOption
	if len(cmd.QresyncParams) >= 2 {
		uidValidity, _ := imap.ParseNumber(cmd.QresyncParams[0])
		lastModseq, _ := imap.ParseNumber64bit(cmd.QresyncParams[1])
		qresyncSelect := &backend.QresyncSelect{
			UIDValidity: uidValidity,
			LastModseq:  lastModseq,
		}
		if len(cmd.QresyncParams) >= 3 {
			uidSet, _ := imap.ParseSeqSet(cmd.QresyncParams[2])
			qresyncSelect.UIDSet = uidSet
		}
		if len(cmd.QresyncParams) >= 5 {
			seqSetPair, _ := imap.ParseSeqSet(cmd.QresyncParams[3])
			uidSetPair, _ := imap.ParseSeqSet(cmd.QresyncParams[4])
			qresyncSelect.SeqSetPair = seqSetPair
			qresyncSelect.UIDSetPair = uidSetPair
		}
		extOpts = append(extOpts, qresyncSelect)
	}
	if cmd.EnableCondstore {
		extOpts = append(extOpts, &backend.CondstoreSelect{Condstore: true})
	}

	_, err = mbox.Select(extOpts)
	if err != nil {
		return err
	}

	items := []imap.StatusItem{
		imap.StatusMessages, imap.StatusRecent, imap.StatusUnseen,
		imap.StatusUidNext, imap.StatusUidValidity, imap.StatusHighestModseq,
	}

	status, extReses, err := mbox.Status(items, extOpts)
	if err != nil {
		return err
	}

	ctx.Mailbox = mbox
	ctx.MailboxReadOnly = cmd.ReadOnly || status.ReadOnly

	res := &responses.Select{Mailbox: status}
	if err = conn.WriteResp(res); err != nil {
		return err
	}

	for _, extRes := range extReses {
		switch extRes := extRes.(type) {
		case *backend.QresyncVanished:
			if err = conn.WriteResp(extRes); err != nil {
				return err
			}
		case *backend.QresyncMessages:
			if err = conn.WriteResp(extRes); err != nil {
				return err
			}
		}
	}

	var code imap.StatusRespCode = imap.CodeReadWrite
	if ctx.MailboxReadOnly {
		code = imap.CodeReadOnly
	}
	return ErrStatusResp(&imap.StatusResp{
		Type: imap.StatusRespOk,
		Code: code,
	})
}

type Unselect struct {
	commands.Unselect
}

func (cmd *Unselect) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}

	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	ctx.Mailbox = nil
	ctx.MailboxReadOnly = false
	return ctx.User.Unselect()
}

type Create struct {
	commands.Create
}

func (cmd *Create) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	return ctx.User.CreateMailbox(cmd.Mailbox)
}

type Delete struct {
	commands.Delete
}

func (cmd *Delete) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	return ctx.User.DeleteMailbox(cmd.Mailbox)
}

type Rename struct {
	commands.Rename
}

func (cmd *Rename) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	return ctx.User.RenameMailbox(cmd.Existing, cmd.New)
}

type Subscribe struct {
	commands.Subscribe
}

func (cmd *Subscribe) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	mbox, err := ctx.User.GetMailbox(cmd.Mailbox)
	if err != nil {
		return err
	}

	return mbox.SetSubscribed(true)
}

type Unsubscribe struct {
	commands.Unsubscribe
}

func (cmd *Unsubscribe) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	mbox, err := ctx.User.GetMailbox(cmd.Mailbox)
	if err != nil {
		return err
	}

	return mbox.SetSubscribed(false)
}

type List struct {
	commands.List
}

func (cmd *List) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	ch := make(chan *imap.MailboxInfo)

	respXGuid := false
	if returnStatus, ok := cmd.Return["STATUS"]; ok {
		for _, returnField := range returnStatus {
			fieldName, _ := imap.ParseString(returnField)
			if strings.ToUpper(fieldName) == "X-GUID" {
				respXGuid = true
				break
			}
		}
	}
	res := &responses.List{Mailboxes: ch, Subscribed: cmd.Subscribed, XGuid: respXGuid}

	done := make(chan error, 1)
	go (func() {
		done <- conn.WriteResp(res)
		// Make sure to drain the channel.
		for range ch {
		}
	})()

	mailboxes, err := ctx.User.ListMailboxes(cmd.Subscribed)
	if err != nil {
		// Close channel to signal end of results
		close(ch)
		return err
	}

	for _, mbox := range mailboxes {
		info, _, err := mbox.Info(nil)
		if err != nil {
			// Close channel to signal end of results
			close(ch)
			return err
		}

		// An empty ("" string) mailbox name argument is a special request to return
		// the hierarchy delimiter and the root name of the name given in the
		// reference.
		if cmd.Mailbox == "" {
			ch <- &imap.MailboxInfo{
				Attributes: []string{imap.NoSelectAttr},
				Delimiter:  info.Delimiter,
				Name:       info.Delimiter,
			}
			break
		}

		if info.Match(cmd.Reference, cmd.Mailbox) {
			ch <- info
		}
	}
	// Close channel to signal end of results
	close(ch)

	return <-done
}

type Status struct {
	commands.Status
}

func (cmd *Status) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	mbox, err := ctx.User.GetMailbox(cmd.Mailbox)
	if err != nil {
		return err
	}

	status, _, err := mbox.Status(cmd.Items, nil)
	if err != nil {
		return err
	}

	// Only keep items thqat have been requested
	items := make(map[imap.StatusItem]interface{})
	for _, k := range cmd.Items {
		items[k] = status.Items[k]
	}
	status.Items = items

	res := &responses.Status{Mailbox: status}
	return conn.WriteResp(res)
}

type Append struct {
	commands.Append
}

func (cmd *Append) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.User == nil {
		return ErrNotAuthenticated
	}

	mbox, err := ctx.User.GetMailbox(cmd.Mailbox)
	if err == backend.ErrNoSuchMailbox {
		return ErrStatusResp(&imap.StatusResp{
			Type: imap.StatusRespNo,
			Code: imap.CodeTryCreate,
			Info: err.Error(),
		})
	} else if err != nil {
		return err
	}

	res, err := mbox.CreateMessage(cmd.Flags, cmd.Date, cmd.Message, nil)
	if err != nil {
		return err
	}

	// If APPEND targets the currently selected mailbox, send an untagged EXISTS
	// Do this only if the backend doesn't send updates itself
	if conn.Server().Updates == nil && ctx.Mailbox != nil && ctx.Mailbox.Name() == mbox.Name() {
		status, _, err := mbox.Status([]imap.StatusItem{imap.StatusMessages}, nil)
		if err != nil {
			return err
		}
		status.Flags = nil
		status.PermanentFlags = nil
		status.UnseenSeqNum = 0

		res := &responses.Select{Mailbox: status}
		if err := conn.WriteResp(res); err != nil {
			return err
		}
	}

	var customResp *imap.StatusResp
	for _, value := range res {
		switch value := value.(type) {
		case backend.AppendUID:
			customResp = &imap.StatusResp{
				Tag:  "",
				Type: imap.StatusRespOk,
				Code: "APPENDUID",
				Arguments: []interface{}{
					value.UIDValidity,
					value.UID,
				},
				Info: "APPEND completed",
			}
		default:
			conn.Server().ErrorLog.Printf("ExtensionResult of unknown type returned by backend: %T", value)
			// Returning an error here would make it look like the command failed.
		}
	}
	if customResp != nil {
		return &imap.ErrStatusResp{Resp: customResp}
	}

	return nil
}
