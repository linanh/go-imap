package server

import (
	"errors"
	"strconv"

	"github.com/linanh/go-imap"
	"github.com/linanh/go-imap/backend"
	"github.com/linanh/go-imap/commands"
	"github.com/linanh/go-imap/responses"
)

// imap errors in Selected state.
var (
	ErrNoMailboxSelected = errors.New("No mailbox selected")
	ErrMailboxReadOnly   = errors.New("Mailbox opened in read-only mode")
)

// A command handler that supports UIDs.
type UidHandler interface {
	Handler

	// Handle this command using UIDs for a given connection.
	UidHandle(conn Conn) error
}

type Check struct {
	commands.Check
}

func (cmd *Check) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}
	if ctx.MailboxReadOnly {
		return ErrMailboxReadOnly
	}

	return ctx.Mailbox.Check()
}

type Close struct {
	commands.Close
}

func (cmd *Close) Handle(conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}

	mailbox := ctx.Mailbox
	ctx.Mailbox = nil
	ctx.MailboxReadOnly = false

	// No need to send expunge updates here, since the mailbox is already unselected
	_, err := mailbox.Expunge(nil)
	return err
}

type Expunge struct {
	commands.Expunge
}

func (cmd *Expunge) Handle(conn Conn) error {
	if cmd.SeqSet != nil {
		return errors.New("Unexpected argment")
	}

	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}
	if ctx.MailboxReadOnly {
		return ErrMailboxReadOnly
	}

	var err error

	// Get a list of messages that will be deleted
	// That will allow us to send expunge updates if the backend doesn't support it
	var seqnums []uint32
	if conn.Server().Updates == nil && !ctx.User.IsEnableQresync() {
		criteria := &imap.SearchCriteria{
			WithFlags: []string{imap.DeletedFlag},
		}
		seqnums, _, err = ctx.Mailbox.SearchMessages(false, criteria, nil)
		if err != nil {
			return err
		}
	}

	var extReses []backend.ExtensionResult
	extReses, err = ctx.Mailbox.Expunge(nil)
	if err != nil {
		return err
	}
	var highestModseq uint64
	for _, extRes := range extReses {
		switch extRes := extRes.(type) {
		case *backend.QresyncVanished:
			if err = conn.WriteResp(extRes); err != nil {
				return err
			}
		case *backend.HighestModseqResp:
			if extRes.HighestModseqResp > highestModseq {
				highestModseq = extRes.HighestModseqResp
			}
		}
	}

	// If the backend doesn't support expunge updates, let's do it ourselves
	if conn.Server().Updates == nil && !ctx.User.IsEnableQresync() {
		done := make(chan error, 1)

		ch := make(chan uint32)
		res := &responses.Expunge{SeqNums: ch}

		go (func() {
			done <- conn.WriteResp(res)
			// Don't need to drain 'ch', sender will stop sending when error written to 'done.
		})()

		// Iterate sequence numbers from the last one to the first one, as deleting
		// messages changes their respective numbers
		for i := len(seqnums) - 1; i >= 0; i-- {
			// Send sequence numbers to channel, and check if conn.WriteResp() finished early.
			select {
			case ch <- seqnums[i]: // Send next seq. number
			case err := <-done: // Check for errors
				close(ch)
				return err
			}
		}
		close(ch)

		if err := <-done; err != nil {
			return err
		}
	}

	if highestModseq > 0 {
		customResp := &imap.StatusResp{
			Type:      imap.StatusRespOk,
			Code:      imap.CodeHighestModseq,
			Arguments: []interface{}{strconv.Itoa(int(highestModseq))},
			Info:      "expunged",
		}
		return &imap.ErrStatusResp{Resp: customResp}
	}

	return nil
}

func (cmd *Expunge) UidHandle(conn Conn) error {
	if _, ok := conn.Server().backendExts["UIDPLUS"]; !ok {
		return errors.New("Unknown command")
	}
	if cmd.SeqSet == nil {
		return errors.New("Missing set argment")
	}

	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}
	if ctx.MailboxReadOnly {
		return ErrMailboxReadOnly
	}

	_, err := ctx.Mailbox.Expunge([]backend.ExtensionOption{
		backend.ExpungeSeqSet{SeqSet: cmd.SeqSet},
	})
	return err
}

type Search struct {
	commands.Search
}

func (cmd *Search) handle(uid bool, conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}

	ids, extReses, err := ctx.Mailbox.SearchMessages(uid, cmd.Criteria, nil)
	if err != nil {
		return err
	}

	res := &responses.Search{Ids: ids}
	for _, extRes := range extReses {
		switch extRes := extRes.(type) {
		case *backend.SearchModseqResp:
			res.Modseq = extRes.Modseq
		}
	}

	return conn.WriteResp(res)
}

func (cmd *Search) Handle(conn Conn) error {
	return cmd.handle(false, conn)
}

func (cmd *Search) UidHandle(conn Conn) error {
	return cmd.handle(true, conn)
}

type Fetch struct {
	commands.Fetch
}

func (cmd *Fetch) handle(uid bool, conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}

	ch := make(chan *imap.Message)
	res := &responses.Fetch{Messages: ch}

	done := make(chan error, 1)
	go (func() {
		done <- conn.WriteResp(res)
		// Make sure to drain the message channel.
		for _ = range ch {
		}
	})()

	var extOpts []backend.ExtensionOption
	if cmd.ChangeSinced > 0 {
		extOpts = append(extOpts, &backend.CondstoreFetch{
			ChangeSinced: cmd.ChangeSinced,
		})
	}
	if cmd.EnableVanished {
		extOpts = append(extOpts, &backend.QresyncFetch{
			EnableVanished: true,
		})
	}
	var err error
	var extReses []backend.ExtensionResult
	if len(extOpts) > 0 {
		extReses, err = ctx.Mailbox.ListMessages(uid, cmd.SeqSet, cmd.Items, ch, extOpts)
	} else {
		_, err = ctx.Mailbox.ListMessages(uid, cmd.SeqSet, cmd.Items, ch, nil)
	}
	if err != nil {
		return err
	}

	for _, extRes := range extReses {
		switch extRes := extRes.(type) {
		case *backend.QresyncVanished:
			if err = conn.WriteResp(extRes); err != nil {
				return err
			}
		}
	}

	return <-done
}

func (cmd *Fetch) Handle(conn Conn) error {
	return cmd.handle(false, conn)
}

func (cmd *Fetch) UidHandle(conn Conn) error {
	// Append UID to the list of requested items if it isn't already present
	hasUid := false
	for _, item := range cmd.Items {
		if item == "UID" {
			hasUid = true
			break
		}
	}
	if !hasUid {
		cmd.Items = append(cmd.Items, "UID")
	}

	return cmd.handle(true, conn)
}

type Store struct {
	commands.Store
}

func (cmd *Store) handle(uid bool, conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}
	if ctx.MailboxReadOnly {
		return ErrMailboxReadOnly
	}

	// Only flags operations are supported
	op, silent, err := imap.ParseFlagsOp(cmd.Item)
	if err != nil {
		return err
	}

	var flags []string

	if flagsList, ok := cmd.Value.([]interface{}); ok {
		// Parse list of flags
		if strs, err := imap.ParseStringList(flagsList); err == nil {
			flags = strs
		} else {
			return err
		}
	} else {
		// Parse single flag
		if str, err := imap.ParseString(cmd.Value); err == nil {
			flags = []string{str}
		} else {
			return err
		}
	}
	for i, flag := range flags {
		flags[i] = imap.CanonicalFlag(flag)
	}

	// If the backend supports message updates, this will prevent this connection
	// from receiving them
	// TODO: find a better way to do this, without conn.silent
	*conn.silent() = silent
	var extReses []backend.ExtensionResult
	if cmd.UnchangedSince > 0 {
		extOpts := []backend.ExtensionOption{
			&backend.CondstoreStore{
				UnchangedSince: cmd.UnchangedSince,
			},
		}
		extReses, err = ctx.Mailbox.UpdateMessagesFlags(uid, cmd.SeqSet, op, flags, extOpts)
	} else {
		extReses, err = ctx.Mailbox.UpdateMessagesFlags(uid, cmd.SeqSet, op, flags, nil)
	}

	for _, extRes := range extReses {
		switch extRes := extRes.(type) {
		case *backend.HighestModseqResp:
			if extRes.HighestModseqResp > 0 {
				if err = conn.WriteResp(extRes); err != nil {
					return err
				}
			}
		//if modseq > unchangedSince
		case *backend.QresyncMessages:
			if err = conn.WriteResp(extRes); err != nil {
				return err
			}
		}
	}

	*conn.silent() = false
	if err != nil {
		return err
	}

	// Not silent: send FETCH updates if the backend doesn't support message
	// updates
	if conn.Server().Updates == nil && !silent {
		inner := &Fetch{}
		inner.SeqSet = cmd.SeqSet
		inner.Items = []imap.FetchItem{imap.FetchFlags}
		if uid {
			inner.Items = append(inner.Items, "UID")
		}

		if cmd.UnchangedSince > 0 {
			inner.Items = append(inner.Items, imap.FetchModseq)
		}

		if err := inner.handle(uid, conn); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *Store) Handle(conn Conn) error {
	return cmd.handle(false, conn)
}

func (cmd *Store) UidHandle(conn Conn) error {
	return cmd.handle(true, conn)
}

type Copy struct {
	commands.Copy
}

func (cmd *Copy) handle(uid bool, conn Conn) error {
	ctx := conn.Context()
	if ctx.Mailbox == nil {
		return ErrNoMailboxSelected
	}

	resp, err := ctx.Mailbox.CopyMessages(uid, cmd.SeqSet, cmd.Mailbox, nil)
	if err != nil {
		if err == backend.ErrNoSuchMailbox {
			return ErrStatusResp(&imap.StatusResp{
				Type: imap.StatusRespNo,
				Code: imap.CodeTryCreate,
				Info: "No such mailbox",
			})
		}
		return err
	}

	var customResp *imap.StatusResp
	for _, value := range resp {
		switch value := value.(type) {
		case backend.CopyUIDs:
			customResp = &imap.StatusResp{
				Type: imap.StatusRespOk,
				Code: "COPYUID",
				Arguments: []interface{}{
					value.UIDValidity,
					value.Source,
					value.Dest,
				},
				Info: "COPY completed",
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

func (cmd *Copy) Handle(conn Conn) error {
	return cmd.handle(false, conn)
}

func (cmd *Copy) UidHandle(conn Conn) error {
	return cmd.handle(true, conn)
}

type Uid struct {
	commands.Uid
}

func (cmd *Uid) Handle(conn Conn) error {
	inner := cmd.Cmd.Command()
	hdlr, err := conn.commandHandler(inner)
	if err != nil {
		return err
	}

	uidHdlr, ok := hdlr.(UidHandler)
	if !ok {
		return errors.New("Command unsupported with UID")
	}

	if err := uidHdlr.UidHandle(conn); err != nil {
		return err
	}

	return ErrStatusResp(&imap.StatusResp{
		Type: imap.StatusRespOk,
		Info: "UID " + inner.Name + " completed",
	})
}
