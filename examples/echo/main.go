// A bot that greets, repeats what it is told and answers its buttons.
//
//	DIALOG_BOT_TOKEN=... go run ./examples/echo
//
// DIALOG_BASE_URL selects another server than the official one.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"

	dialog "github.com/DialogPM/dialog-go"
)

// A 1x1 PNG, so the example needs no file.
const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func main() {
	base := os.Getenv("DIALOG_BASE_URL")
	if base == "" {
		base = dialog.DefaultBaseURL
	}
	bot := dialog.New(os.Getenv("DIALOG_BOT_TOKEN"), dialog.WithBaseURL(base))
	ctx := context.Background()
	me, err := bot.GetMe(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("running as @" + me.Username)
	if err := bot.SetMyCommands(ctx, []dialog.Command{{Command: "start", Description: "Say hello"}, {Command: "buttons", Description: "Show two buttons"}, {Command: "photo", Description: "Send a picture"}}); err != nil {
		log.Fatal(err)
	}

	err = bot.Updates(ctx, func(ctx context.Context, u dialog.Update) error {
		var err error
		switch {
		case u.Message != nil && u.Message.Command() == "start":
			_, err = bot.SendMessage(ctx, u.Message.ChatID, "Hello from Go", nil)
		case u.Message != nil && u.Message.Command() == "photo":
			png, _ := base64.StdEncoding.DecodeString(pixel)
			_, err = bot.SendPhoto(ctx, u.Message.ChatID, png, &dialog.SendOptions{Caption: "A picture"})
		case u.Message != nil && u.Message.Command() == "buttons":
			_, err = bot.SendMessage(ctx, u.Message.ChatID, "Pick one", &dialog.SendOptions{
				Buttons: [][]dialog.Button{dialog.Row(dialog.Callback("Yes", "yes"), dialog.Callback("No", "no"))},
			})
		case u.Message != nil:
			_, err = bot.SendMessage(ctx, u.Message.ChatID, "You said: "+u.Message.Text, nil)
		case u.CallbackQuery != nil:
			_, err = bot.SendMessage(ctx, u.CallbackQuery.ChatID, "You pressed "+u.CallbackQuery.Data, nil)
		}
		if dialog.IsStopped(err) {
			return nil
		}
		return err
	})
	log.Fatal(err)
}
