package slack

import (
	"strings"
	"time"

	"gopkg.in/inconshreveable/log15.v2"

	"github.com/mvader/slack"
	"github.com/src-d/flamingo"
)

type botConversation struct {
	bot      string
	channel  flamingo.Channel
	rtm      slackRTM
	actions  chan slack.AttachmentActionCallback
	messages chan *slack.MessageEvent
	shutdown chan struct{}
	closed   chan struct{}
	delegate handlerDelegate
}

func newBotConversation(bot, channelID string, rtm slackRTM, delegate handlerDelegate) (*botConversation, error) {
	var channel flamingo.Channel
	// Channel IDs prefixed with C are channels,
	// prefixed with G are groups and prefixed with D are directs
	if strings.HasPrefix(channelID, "C") {
		ch, err := rtm.GetChannelInfo(channelID)
		if err != nil {
			return nil, err
		}

		channel = flamingo.Channel{
			ID:    ch.ID,
			Name:  ch.Name,
			Type:  flamingo.SlackClient,
			IsDM:  !ch.IsChannel,
			Extra: ch,
		}
	} else {
		channel = flamingo.Channel{
			ID:   channelID,
			Type: flamingo.SlackClient,
			IsDM: true,
		}
	}

	return &botConversation{
		rtm:      rtm,
		bot:      bot,
		channel:  channel,
		actions:  make(chan slack.AttachmentActionCallback),
		messages: make(chan *slack.MessageEvent),
		shutdown: make(chan struct{}, 1),
		closed:   make(chan struct{}, 1),
		delegate: delegate,
	}, nil
}

func (c *botConversation) run() {
	for {
		select {
		case <-c.shutdown:
			c.closed <- struct{}{}
			return
		case msg := <-c.messages:
			message, err := c.convertMessage(msg)
			if err != nil {
				log15.Error("error converting message", "err", err.Error())
				continue
			}

			ctrl, ok := c.delegate.ControllerFor(message)
			if !ok {
				log15.Warn("no controller for message", "text", message.Text)
				continue
			}

			if err := ctrl.Handle(c.createBot(), message); err != nil {
				log15.Error("error handling message", "error", err.Error())
			}

		case action := <-c.actions:
			handler, ok := c.delegate.ActionHandler(action.CallbackID)
			if !ok {
				log15.Warn("no handler for callback", "id", action.CallbackID)
				continue
			}

			handler(c.createBot(), convertAction(action))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (c *botConversation) createBot() flamingo.Bot {
	return &bot{
		id:      c.bot,
		channel: c.channel,
		api:     c.rtm,
		msgs:    c.messages,
		actions: c.actions,
	}
}

func (c *botConversation) convertMessage(src *slack.MessageEvent) (flamingo.Message, error) {
	var userID = src.Msg.User
	if userID == "" {
		userID = src.Msg.BotID
	}

	user, err := c.rtm.GetUserInfo(userID)
	if err != nil {
		log15.Error("unable to find user", "id", userID)
		return flamingo.Message{}, err
	}

	return newMessage(flamingo.User{
		ID:       userID,
		Username: user.Name,
		Name:     user.RealName,
		IsBot:    user.IsBot,
		Type:     flamingo.SlackClient,
		Extra:    user,
	}, c.channel, src.Msg), nil
}

func (c *botConversation) handleIntro() {
	c.delegate.HandleIntro(c.createBot(), c.channel)
}

func (c *botConversation) handleJob(job flamingo.Job) {
	if err := job(c.createBot(), c.channel); err != nil {
		log15.Error("error running job", "bot", c.bot, "channel", c.channel.ID, "err", err.Error())
	}
}

func (c *botConversation) stop() {
	c.shutdown <- struct{}{}
	close(c.shutdown)
	<-c.closed
	close(c.closed)
	close(c.actions)
	close(c.messages)
}
