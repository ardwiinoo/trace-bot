// Package telegram is the Telegram channel adapter (long polling).
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tracebot/internal/agent"
)

const telegramLimit = 4096

type Adapter struct {
	agent   *agent.Agent
	allowed map[int64]bool
}

// Run blocks until ctx is cancelled.
func Run(ctx context.Context, token string, allowed map[int64]bool, ag *agent.Agent) error {
	a := &Adapter{agent: ag, allowed: allowed}
	b, err := bot.New(token, bot.WithDefaultHandler(a.handle))
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		slog.Warn("TELEGRAM_ALLOWED_USER_IDS is empty: the bot will refuse everyone and reply with their user id")
	}
	slog.Info("telegram bot started")
	b.Start(ctx)
	return nil
}

func (a *Adapter) handle(ctx context.Context, b *bot.Bot, u *models.Update) {
	if u.Message == nil || u.Message.From == nil || u.Message.Text == "" {
		return
	}
	go a.reply(ctx, b, u.Message)
}

func (a *Adapter) reply(ctx context.Context, b *bot.Bot, m *models.Message) {
	send := func(text string) {
		for _, chunk := range split(text, telegramLimit) {
			if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: m.Chat.ID, Text: chunk}); err != nil {
				slog.Error("telegram send", "err", err)
				return
			}
		}
	}

	if !a.allowed[m.From.ID] {
		send(fmt.Sprintf("Kamu belum diizinkan memakai bot ini.\nUser ID kamu: %d\nTambahkan ke TELEGRAM_ALLOWED_USER_IDS di .env lalu restart bot.", m.From.ID))
		return
	}

	session := "tg-" + strconv.FormatInt(m.Chat.ID, 10)
	text := strings.TrimSpace(m.Text)
	switch {
	case strings.HasPrefix(text, "/start"), strings.HasPrefix(text, "/help"):
		send("Halo! Tanya apa saja soal log dan metrics service kamu, misalnya:\n- ada error jam 2 pagi tadi?\n- jam berapa request paling tinggi hari ini?\n- endpoint mana yang paling lambat?\n\n/reset untuk memulai percakapan baru.")
		return
	case strings.HasPrefix(text, "/reset"):
		a.agent.Reset(session)
		send("Percakapan direset.")
		return
	}

	// Keep the typing indicator alive while the agent works.
	typingCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		for {
			b.SendChatAction(typingCtx, &bot.SendChatActionParams{ChatID: m.Chat.ID, Action: models.ChatActionTyping})
			select {
			case <-typingCtx.Done():
				return
			case <-timeAfter4s():
			}
		}
	}()

	res, err := a.agent.Ask(ctx, session, text)
	stop()
	if err != nil {
		slog.Error("ask failed", "err", err)
		send("Terjadi error: " + err.Error())
		return
	}

	out := res.Answer
	if len(res.Queries) > 0 {
		var q strings.Builder
		q.WriteString("\n\nQuery yang dijalankan:")
		for _, c := range res.Queries {
			fmt.Fprintf(&q, "\n- %s %s", c.Tool, c.Args)
		}
		out += q.String()
	}
	send(out)
}

// split breaks text into chunks no longer than max bytes, preferring newlines.
func split(text string, max int) []string {
	var chunks []string
	for len(text) > max {
		cut := strings.LastIndex(text[:max], "\n")
		if cut <= 0 {
			cut = max
		}
		chunks = append(chunks, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	return append(chunks, text)
}
