package dialog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type call struct {
	path, contentType string
	query             map[string][]string
	body              []byte
}

// stub answers each call with the next prepared answer.
type stub struct {
	*httptest.Server
	calls   []call
	answers []func(http.ResponseWriter)
}

func newStub(t *testing.T) (*stub, *Bot) {
	s := &stub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.calls = append(s.calls, call{r.URL.EscapedPath(), r.Header.Get("Content-Type"), r.URL.Query(), body})
		w.Header().Set("Content-Type", "application/json")
		if len(s.answers) == 0 {
			io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		a := s.answers[0]
		s.answers = s.answers[1:]
		a(w)
	}))
	t.Cleanup(s.Close)
	return s, New("tok/en", WithBaseURL(s.URL+"/"))
}

func (s *stub) ok(result string) {
	s.answers = append(s.answers, func(w http.ResponseWriter) { io.WriteString(w, `{"ok":true,"result":`+result+`}`) })
}

func (s *stub) last() call { return s.calls[len(s.calls)-1] }

func jsonOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("not JSON: %s", b)
	}
	return v
}

func TestSendMessageWithButtons(t *testing.T) {
	s, bot := newStub(t)
	s.ok(`{"message_id":"m1","date":7,"chat":{"id":"c1"}}`)
	sent, err := bot.SendMessage(context.Background(), "c1", "Motion", &SendOptions{
		Buttons: [][]Button{Row(Callback("Ignore", "ignore:7"), Link("Live", "https://example.org"))},
		Silent:  true,
	})
	if err != nil || sent.MessageID != "m1" || sent.ChatID != "c1" || sent.Date.Unix() != 7 {
		t.Fatalf("sent = %+v, %v", sent, err)
	}
	if got := s.last().path; got != "/bot/tok%2Fen/sendMessage" {
		t.Fatalf("path = %s", got)
	}
	want := map[string]any{"chat_id": "c1", "text": "Motion", "silent": true, "buttons": []any{[]any{
		map[string]any{"text": "Ignore", "data": "ignore:7"}, map[string]any{"text": "Live", "url": "https://example.org"},
	}}}
	if got := jsonOf(t, s.last().body); !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %v", got)
	}
}

func TestSendPhotoSendsTheBytes(t *testing.T) {
	s, bot := newStub(t)
	s.ok(`{"message_id":"m2","date":8,"chat":{"id":"c1"}}`)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 20)...)
	if _, err := bot.SendPhoto(context.Background(), "c1", png, &SendOptions{Caption: "Front door", Buttons: [][]Button{{Callback("Ok", "ok")}}}); err != nil {
		t.Fatal(err)
	}
	c := s.last()
	if !strings.HasPrefix(c.contentType, "image/") || string(c.body) != string(png) {
		t.Fatalf("content type %q, %d bytes", c.contentType, len(c.body))
	}
	if c.query["chat_id"][0] != "c1" || c.query["caption"][0] != "Front door" || c.query["buttons"][0] != `[[{"text":"Ok","data":"ok"}]]` {
		t.Fatalf("query = %v", c.query)
	}
}

func TestUpdates(t *testing.T) {
	s, bot := newStub(t)
	s.ok(`[{"update_id":5,"message":{"message_id":"u1","chat":{"id":"c1"},"date":1,"text":"/Status now"}},
	       {"update_id":6,"callback_query":{"chat":{"id":"c1"},"message_id":"m1","data":"ignore:7","date":2}},
	       {"update_id":7,"chat_stopped":{"chat":{"id":"c1"},"date":3}}]`)
	ups, err := bot.GetUpdates(context.Background(), 5, 0, 99*time.Second)
	if err != nil || len(ups) != 3 {
		t.Fatalf("%v %v", ups, err)
	}
	if got := jsonOf(t, s.last().body); got["offset"] != float64(5) || got["limit"] != float64(100) || got["timeout"] != float64(50) {
		t.Fatalf("body = %v", got)
	}
	if m := ups[0].Message; m == nil || m.Command() != "status" || m.ChatID != "c1" {
		t.Fatalf("message = %+v", m)
	}
	if c := ups[1].CallbackQuery; c == nil || c.Data != "ignore:7" || c.MessageID != "m1" {
		t.Fatalf("callback = %+v", c)
	}
	if st := ups[2].ChatStopped; st == nil || st.ChatID != "c1" {
		t.Fatalf("stopped = %+v", st)
	}
}

func TestTheLoopConfirmsWhatItHandled(t *testing.T) {
	s, bot := newStub(t)
	s.ok(`[{"update_id":3,"message":{"message_id":"a","chat":{"id":"c"},"date":1,"text":"hi"}}]`)
	s.ok(`[{"update_id":4,"message":{"message_id":"b","chat":{"id":"c"},"date":1,"text":"there"}}]`)
	done := errors.New("done")
	var texts []string
	err := bot.Updates(context.Background(), func(_ context.Context, u Update) error {
		texts = append(texts, u.Message.Text)
		if len(texts) == 2 {
			return done
		}
		return nil
	})
	if err != done || !reflect.DeepEqual(texts, []string{"hi", "there"}) {
		t.Fatalf("%v %v", texts, err)
	}
	if a, b := jsonOf(t, s.calls[0].body)["offset"], jsonOf(t, s.calls[1].body)["offset"]; a != float64(0) || b != float64(4) {
		t.Fatalf("offsets %v %v", a, b)
	}
}

func TestErrorsSayWhy(t *testing.T) {
	s, bot := newStub(t)
	s.answers = append(s.answers, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"forbidden","message":"the user stopped the bot","reason":"bot_stopped"}`)
	})
	_, err := bot.SendMessage(context.Background(), "c1", "hello", nil)
	var e *Error
	if !errors.As(err, &e) || e.Status != 403 || e.Code != "forbidden" || !IsStopped(err) {
		t.Fatalf("err = %v", err)
	}
	// A wrong token ends the loop instead of being retried forever.
	s.answers = append(s.answers, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"unauthorized","message":"invalid bot token"}`)
	})
	if err := bot.Updates(context.Background(), func(context.Context, Update) error { return nil }); !errors.As(err, &e) || e.Status != 401 {
		t.Fatalf("loop err = %v", err)
	}
}

func TestNetworkErrorsDoNotShowTheToken(t *testing.T) {
	bot := New("secret-token", WithBaseURL("http://127.0.0.1:1"))
	_, err := bot.GetMe(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err = %v", err)
	}
}

func TestCommands(t *testing.T) {
	s, bot := newStub(t)
	if err := bot.SetMyCommands(context.Background(), []Command{{"status", "How things are"}}); err != nil {
		t.Fatal(err)
	}
	if got := string(s.last().body); got != `{"commands":[{"command":"status","description":"How things are"}]}` {
		t.Fatalf("body = %s", got)
	}
	s.ok(`{"id":"b1","username":"weather_bot","name":"Weather","about":"Rain","commands":[{"command":"status","description":"d"}]}`)
	me, err := bot.GetMe(context.Background())
	if err != nil || me.Username != "weather_bot" || len(me.Commands) != 1 {
		t.Fatalf("%+v %v", me, err)
	}
}
