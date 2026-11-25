# dialog-go

Go client for the Bot API of [Dialog](https://dialog.pm), the private messenger. Send
yourself (or anyone who starts your bot) a notification from a program, a server or a cron
job: text, pictures and buttons.

- Standard library only, Go 1.21+.
- A bot writes only to people who started it, and stops when they stop it.
- The bot never learns who it is talking to: a chat id is random and different for every bot.

## Install

```
go get github.com/DialogPM/dialog-go
```

## Create a bot

In the Dialog app: **Settings → Bots → New bot**. You get a token and a public link
(`dialog.pm/@your_bot`). Open the link, press **Start**, and the chat id appears in the
list of subscribers.

## Send a message

```go
import dialog "github.com/DialogPM/dialog-go"

bot := dialog.New("TOKEN")
_, err := bot.SendMessage(ctx, "CHAT_ID", "Backup finished", nil)
```

Pictures and buttons:

```go
photo, _ := os.ReadFile("door.jpg")
_, err := bot.SendPhoto(ctx, "CHAT_ID", photo, &dialog.SendOptions{
	Caption: "*Motion at the door*",
	Buttons: [][]dialog.Button{dialog.Row(
		dialog.Callback("Ignore", "ignore"),
		dialog.Link("Live view", "https://example.org/cam"),
	)},
})
```

## Answer people

```go
err := bot.Updates(ctx, func(ctx context.Context, u dialog.Update) error {
	switch {
	case u.Message != nil:
		_, err := bot.SendMessage(ctx, u.Message.ChatID, "You said: "+u.Message.Text, nil)
		return err
	case u.CallbackQuery != nil:
		_, err := bot.SendMessage(ctx, u.CallbackQuery.ChatID, "You pressed "+u.CallbackQuery.Data, nil)
		return err
	}
	return nil
})
```

`Updates` long-polls forever, confirms each update once the handler returned nil, retries
network errors and returns when the context ends or the server refuses the bot.

Commands offered after `/` in the chat:

```go
bot.SetMyCommands(ctx, []dialog.Command{{Command: "status", Description: "How things are"}})
```

## Errors

A refused call returns a `*dialog.Error` with `Status`, `Code`, `Message` and, when the
server gives one, a `Reason`:

| `Reason` | Meaning |
|---|---|
| `bot_stopped` | The user stopped the bot (`dialog.IsStopped(err)`). Do not write to this chat until it sends `/start` again. |
| `chat_not_found` | Not a chat of this bot. |
| `bot_disabled` | The server operator switched the bot off. |

A `429` carries `RetryAfter`.

## Another server

Dialog can be self-hosted:

```go
bot := dialog.New("TOKEN", dialog.WithBaseURL("https://chat.example.org"))
```

## Good to know

Messages exchanged with bots are **not** end-to-end encrypted: the bot's program and the
server can read them. Everything else in Dialog is.

## License

MIT
