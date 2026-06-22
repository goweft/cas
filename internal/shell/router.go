package shell

import (
	"context"

	"github.com/goweft/cas/internal/intent"
)

// ProcessMessage classifies the message, calls the LLM, and returns a Response.
func (sh *Shell) ProcessMessage(ctx context.Context, sessionID, message string) (*Response, error) {
	sess, err := sh.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Check plugin commands first — user-defined overrides
	if cmd, ok := sh.plugins.Match(message); ok {
		userMsg := sess.addMessage("user", message)
		if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
			return nil, err
		}
		return sh.handlePlugin(sess, cmd)
	}

	in := intent.Detect(message)
	userMsg := sess.addMessage("user", message)
	if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
		return nil, err
	}

	var resp *Response
	switch in.Kind {
	case intent.KindCreate:
		resp, err = sh.handleCreate(ctx, sess, in, message)
	case intent.KindEdit:
		resp, err = sh.handleEdit(ctx, sess, message)
	case intent.KindClose:
		resp, err = sh.handleClose(sess)
	case intent.KindRun:
		resp, err = sh.handleRun(ctx, sess)
	case intent.KindCombine:
		resp, err = sh.handleCombine(ctx, sess, message)
	case intent.KindIngest:
		resp, err = sh.handleIngest(ctx, sess, in)
	case intent.KindOrchestrate:
		resp, err = sh.handleOrchestrate(ctx, sess, message)
	case intent.KindReconnect:
		resp, err = sh.handleReconnect(ctx, sess, in)
	case intent.KindBrowse:
		resp, err = sh.handleBrowse(ctx, sess, in)
	default:
		resp, err = sh.handleChat(ctx, sess, message)
	}
	if err != nil {
		return nil, err
	}

	shellMsg := sess.addMessage("shell", resp.ChatReply)
	if err := sh.store.SaveMessage(toStoreMsg(shellMsg)); err != nil {
		return nil, err
	}

	wsTitle, wsType := "", ""
	if resp.Workspace != nil {
		wsTitle, wsType = resp.Workspace.Title, resp.Workspace.Type
	}
	sh.conductor.Observe(string(in.Kind), message, wsTitle, wsType)

	return resp, nil
}

// StreamMessage classifies the message, streams tokens via onToken,
// and returns a StreamResponse when generation finishes.
func (sh *Shell) StreamMessage(ctx context.Context, sessionID, message string, onToken func(string)) (*StreamResponse, error) {
	sess, err := sh.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Check plugin commands first
	if cmd, ok := sh.plugins.Match(message); ok {
		userMsg := sess.addMessage("user", message)
		if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
			return nil, err
		}
		r, err := sh.handlePlugin(sess, cmd)
		if err != nil {
			return nil, err
		}
		return &StreamResponse{ChatReply: r.ChatReply, Intent: r.Intent}, nil
	}

	in := intent.Detect(message)
	userMsg := sess.addMessage("user", message)
	if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
		return nil, err
	}

	var resp *StreamResponse
	switch in.Kind {
	case intent.KindCreate:
		resp, err = sh.streamCreate(ctx, sess, in, message, onToken)
	case intent.KindEdit:
		resp, err = sh.streamEdit(ctx, sess, message, onToken)
	case intent.KindClose:
		r, e := sh.handleClose(sess)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindRun:
		r, e := sh.handleRun(ctx, sess)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindCombine:
		resp, err = sh.streamCombine(ctx, sess, message, onToken)
	case intent.KindIngest:
		r, e := sh.handleIngest(ctx, sess, in)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindOrchestrate:
		r, e := sh.handleOrchestrate(ctx, sess, message)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindReconnect:
		r, e := sh.handleReconnect(ctx, sess, in)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindBrowse:
		r, e := sh.handleBrowse(ctx, sess, in)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	default:
		resp, err = sh.streamChat(ctx, sess, message, onToken)
	}
	if err != nil {
		return nil, err
	}

	shellMsg := sess.addMessage("shell", resp.ChatReply)
	if err := sh.store.SaveMessage(toStoreMsg(shellMsg)); err != nil {
		return nil, err
	}

	wsTitle, wsType := "", ""
	if resp.Workspace != nil {
		wsTitle, wsType = resp.Workspace.Title, resp.Workspace.Type
	}
	sh.conductor.Observe(string(in.Kind), message, wsTitle, wsType)

	return resp, nil
}

// ── Handlers ──────────────────────────────────────────────────────
