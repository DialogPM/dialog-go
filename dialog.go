// Package dialog is a client for the Bot API of Dialog (https://dialog.pm), the private
// messenger.
//
// A bot is created in the Dialog app (Settings > Bots), which gives it a token. With the
// token a program can write to the people who started the bot: text, pictures and buttons.
//
//	bot := dialog.New("TOKEN")
//	_, err := bot.SendMessage(ctx, chatID, "Backup finished", nil)
//
// A bot can write only to a chat its user started and has not stopped, and it never learns
// who that user is: a chat id is random and different for every bot. Messages exchanged
// with bots are not end-to-end encrypted.
package dialog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the official Dialog server.
const DefaultBaseURL = "https://chat.dialog.pm"

const userAgent = "dialog-go/0.1.0"

// The server holds a getUpdates call open for at most this long.
const maxPoll = 50 * time.Second

// Bot is a bot, by its token. It is safe for concurrent use.
type Bot struct {
	token string
	base  string
	http  *http.Client
}

// Option changes how a Bot talks to the server.
type Option func(*Bot)

// WithBaseURL selects another Dialog server than the official one (self-hosting).
func WithBaseURL(u string) Option { return func(b *Bot) { b.base = strings.TrimRight(u, "/") } }

// WithHTTPClient uses c for every call. Its Timeout, if any, must leave room for long
// polling: Updates waits up to 50 seconds for one answer.
func WithHTTPClient(c *http.Client) Option { return func(b *Bot) { b.http = c } }

// New returns the bot of token.
func New(token string, opts ...Option) *Bot {
	b := &Bot{token: token, base: DefaultBaseURL, http: &http.Client{}}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Error is a call the server refused.
type Error struct {
	Status  int    // HTTP status
	Code    string // the server's error code: "forbidden", "rate_limited"...
	Message string
	// Reason says why in a way a program can act on, when the server gives one:
	// "chat_not_found", "bot_stopped", "bot_disabled".
	Reason     string
	RetryAfter time.Duration // with status 429
}

func (e *Error) Error() string {
	s := fmt.Sprintf("dialog: %d %s: %s", e.Status, e.Code, e.Message)
	if e.Reason != "" {
		s += " (" + e.Reason + ")"
	}
	return s
}

// IsStopped reports whether err says the user stopped the bot: nothing can be sent to that
// chat until they start it again.
func IsStopped(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == "bot_stopped"
}

// Command is a command the apps offer after "/" in the chat with the bot.
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Info describes the bot.
type Info struct {
	ID       string    `json:"id"`
	Username string    `json:"username"`
	Name     string    `json:"name"`
	About    string    `json:"about"`
	Commands []Command `json:"commands"`
}

// Button is a button under a message: made with Callback or Link.
type Button struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Callback is a button that sends data (at most 64 bytes) back to the bot as a
// CallbackQuery when pressed.
func Callback(text, data string) Button { return Button{Text: text, Data: data} }

// Link is a button that opens an https address, after the app shows it.
func Link(text, url string) Button { return Button{Text: text, URL: url} }

// Row is one row of buttons, for SendOptions.Buttons.
func Row(buttons ...Button) []Button { return buttons }

// SendOptions are the optional parts of a send. A nil *SendOptions means none.
type SendOptions struct {
	Buttons [][]Button // rows of buttons under the message
	Silent  bool       // deliver without sound
	Caption string     // SendPhoto only: the text under the picture
}

// Sent is a message the server accepted.
type Sent struct {
	MessageID string
	ChatID    string
	Date      time.Time
}

// Message is something a person wrote to the bot. The first message of a chat is "/start".
type Message struct {
	MessageID string
	ChatID    string
	Date      time.Time
	Text      string
}

// Command returns the command of the message without the slash ("/Status now" ->
// "status"), or "" when it is not a command.
func (m *Message) Command() string {
	if !strings.HasPrefix(m.Text, "/") {
		return ""
	}
	f := strings.Fields(m.Text[1:])
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

// CallbackQuery is a press of a button of the message MessageID.
type CallbackQuery struct {
	ChatID    string
	MessageID string
	Data      string
	Date      time.Time
}

// ChatStopped says a person stopped the bot: it can no longer write to ChatID.
type ChatStopped struct {
	ChatID string
	Date   time.Time
}

// Update is one event for the bot: exactly one of Message, CallbackQuery and ChatStopped
// is set.
type Update struct {
	ID            int64
	Message       *Message
	CallbackQuery *CallbackQuery
	ChatStopped   *ChatStopped
}

// GetMe returns the bot itself: a quick way to check the token.
func (b *Bot) GetMe(ctx context.Context) (*Info, error) {
	var info Info
	return &info, b.call(ctx, "getMe", map[string]any{}, &info)
}

// SendMessage sends text (at most 4096 characters). *bold*, _italic_ and `code` are
// rendered. IsStopped tells when the user stopped the bot.
func (b *Bot) SendMessage(ctx context.Context, chatID, text string, opts *SendOptions) (*Sent, error) {
	params := map[string]any{"chat_id": chatID, "text": text}
	if opts != nil {
		if len(opts.Buttons) > 0 {
			params["buttons"] = opts.Buttons
		}
		if opts.Silent {
			params["silent"] = true
		}
	}
	var raw rawSent
	if err := b.call(ctx, "sendMessage", params, &raw); err != nil {
		return nil, err
	}
	return raw.sent(), nil
}

// SendPhoto sends a picture: the bytes of a JPEG, PNG, GIF or WebP of at most 10 MiB.
func (b *Bot) SendPhoto(ctx context.Context, chatID string, photo []byte, opts *SendOptions) (*Sent, error) {
	q := url.Values{"chat_id": {chatID}}
	if opts != nil {
		if opts.Caption != "" {
			q.Set("caption", opts.Caption)
		}
		if len(opts.Buttons) > 0 {
			rows, err := json.Marshal(opts.Buttons)
			if err != nil {
				return nil, err
			}
			q.Set("buttons", string(rows))
		}
		if opts.Silent {
			q.Set("silent", "true")
		}
	}
	var raw rawSent
	if err := b.do(ctx, "sendPhoto?"+q.Encode(), "image/*", photo, &raw); err != nil {
		return nil, err
	}
	return raw.sent(), nil
}

// GetUpdates returns what happened since offset: messages, button presses, stops.
//
// Updates below offset are confirmed and deleted on the server, so pass the last ID handled
// plus one. With a wait (at most 50 seconds) the call waits for an update instead of
// returning none. Updates does all of this.
func (b *Bot) GetUpdates(ctx context.Context, offset int64, limit int, wait time.Duration) ([]Update, error) {
	if wait > maxPoll {
		wait = maxPoll
	}
	if wait < 0 {
		wait = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var raw []rawUpdate
	params := map[string]any{"offset": offset, "limit": limit, "timeout": int(wait / time.Second)}
	if err := b.call(ctx, "getUpdates", params, &raw); err != nil {
		return nil, err
	}
	out := make([]Update, len(raw))
	for i, r := range raw {
		out[i] = r.update()
	}
	return out, nil
}

// Updates calls handle for every update, forever (long polling), confirming each one once
// handle returned nil. It returns when ctx ends, when handle returns an error, or when the
// server refuses the bot (a wrong token, for instance); network errors are retried.
func (b *Bot) Updates(ctx context.Context, handle func(context.Context, Update) error) error {
	var offset int64
	pause := time.Second
	for {
		ups, err := b.GetUpdates(ctx, offset, 100, 30*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var e *Error
			if errors.As(err, &e) && e.Status != http.StatusTooManyRequests && e.Status < 500 {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
			if pause < 30*time.Second {
				pause *= 2
			}
			continue
		}
		pause = time.Second
		for _, u := range ups {
			if err := handle(ctx, u); err != nil {
				return err
			}
			offset = u.ID + 1
		}
	}
}

// SetMyCommands sets the commands the apps offer after "/" (at most 50).
func (b *Bot) SetMyCommands(ctx context.Context, commands []Command) error {
	if commands == nil {
		commands = []Command{}
	}
	return b.call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil)
}

// GetMyCommands returns the bot's commands.
func (b *Bot) GetMyCommands(ctx context.Context) ([]Command, error) {
	var out []Command
	return out, b.call(ctx, "getMyCommands", map[string]any{}, &out)
}

type rawChat struct {
	ID string `json:"id"`
}

type rawSent struct {
	MessageID string  `json:"message_id"`
	Date      int64   `json:"date"`
	Chat      rawChat `json:"chat"`
}

func (r rawSent) sent() *Sent {
	return &Sent{MessageID: r.MessageID, ChatID: r.Chat.ID, Date: time.Unix(r.Date, 0)}
}

type rawUpdate struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		MessageID string  `json:"message_id"`
		Chat      rawChat `json:"chat"`
		Date      int64   `json:"date"`
		Text      string  `json:"text"`
	} `json:"message"`
	Callback *struct {
		Chat      rawChat `json:"chat"`
		MessageID string  `json:"message_id"`
		Data      string  `json:"data"`
		Date      int64   `json:"date"`
	} `json:"callback_query"`
	Stopped *struct {
		Chat rawChat `json:"chat"`
		Date int64   `json:"date"`
	} `json:"chat_stopped"`
}

func (r rawUpdate) update() Update {
	u := Update{ID: r.ID}
	if m := r.Message; m != nil {
		u.Message = &Message{MessageID: m.MessageID, ChatID: m.Chat.ID, Date: time.Unix(m.Date, 0), Text: m.Text}
	}
	if c := r.Callback; c != nil {
		u.CallbackQuery = &CallbackQuery{ChatID: c.Chat.ID, MessageID: c.MessageID, Data: c.Data, Date: time.Unix(c.Date, 0)}
	}
	if s := r.Stopped; s != nil {
		u.ChatStopped = &ChatStopped{ChatID: s.Chat.ID, Date: time.Unix(s.Date, 0)}
	}
	return u
}

func (b *Bot) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return b.do(ctx, method, "application/json", body, out)
}

func (b *Bot) do(ctx context.Context, methodAndQuery, contentType string, body []byte, out any) error {
	if b.token == "" {
		return errors.New("dialog: a bot token is required")
	}
	u := b.base + "/bot/" + url.PathEscape(b.token) + "/" + methodAndQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return errors.New("dialog: invalid server address")
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", userAgent)
	resp, err := b.http.Do(req)
	if err != nil {
		// A *url.Error prints the address, which holds the token.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("dialog: %s: %w", strings.SplitN(methodAndQuery, "?", 2)[0], err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("dialog: reading the answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = "http_error"
		}
		out := &Error{Status: resp.StatusCode, Code: e.Error, Message: e.Message, Reason: e.Reason}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			out.RetryAfter = time.Duration(s) * time.Second
		}
		return out
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || !envelope.OK {
		return &Error{Status: resp.StatusCode, Code: "bad_response", Message: "the server's answer is not a Bot API result"}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}
