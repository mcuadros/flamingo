package slack

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mvader/slack"
	"github.com/src-d/flamingo"
	"github.com/stretchr/testify/require"
)

var existentChannel string = "existentChannel"

type slackRTMMock struct {
	events chan slack.RTMEvent
	apiMock
}

func newSlackRTMMock() *slackRTMMock {
	return &slackRTMMock{
		apiMock: apiMock{
			users: make(map[string]*slack.User),
		},
	}
}

func (m *slackRTMMock) IncomingEvents() chan slack.RTMEvent {
	return m.events
}

func (m *slackRTMMock) setUser(user *slack.User) {
	m.apiMock.users[user.Name] = user
}

func TestHandleAction(t *testing.T) {
	require := require.New(t)

	client := newBotClient(
		"aaaa",
		newSlackRTMMock(),
		NewClient("", ClientOptions{Debug: true}).(*slackClient),
	)
	defer client.stop()

	convo := &botConversation{
		actions:  make(chan slack.AttachmentActionCallback, 1),
		shutdown: make(chan struct{}, 1),
		closed:   make(chan struct{}, 1),
		messages: make(chan *slack.MessageEvent, 1),
	}
	client.conversations["bbbb"] = convo
	go convo.run()

	client.handleAction("bbbb", slack.AttachmentActionCallback{
		CallbackID: "foo",
	})

	select {
	case action := <-convo.actions:
		require.Equal("foo", action.CallbackID)
	case <-time.After(50 * time.Millisecond):
		require.FailNow("action was not received by conversation")
	}

	client.handleAction("cccc", slack.AttachmentActionCallback{
		CallbackID: "bar",
	})

	select {
	case <-convo.actions:
		require.FailNow("action should not have been received by conversation")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandleActionInitMessage(t *testing.T) {
	require := require.New(t)
	_, helloCont, _, botCli := getSlackMocks()
	defer botCli.stop()

	botCli.handleAction(existentChannel, slack.AttachmentActionCallback{
		CallbackID: "foo",
	})
	conv := botCli.conversations[existentChannel]
	<-conv.actions
	require.Equal(1, openedConversationsCount(botCli))
	require.Equal(0, calledIntroCount(helloCont))

	botCli.handleAction("notExistentChannel2", slack.AttachmentActionCallback{
		CallbackID: "foo",
	})
	conv2 := botCli.conversations["notExistentChannel2"]
	<-conv2.actions
	require.Equal(2, openedConversationsCount(botCli))
	require.Equal(1, calledIntroCount(helloCont))
}

func TestHandleRTMEvent(t *testing.T) {
	require := require.New(t)
	mock := &slackRTMMock{
		events: make(chan slack.RTMEvent),
	}

	client := newBotClient(
		"aaaa",
		mock,
		NewClient("", ClientOptions{Debug: true}).(*slackClient),
	)
	defer client.stop()

	convo := &botConversation{
		actions:  make(chan slack.AttachmentActionCallback, 1),
		shutdown: make(chan struct{}, 1),
		closed:   make(chan struct{}, 1),
		messages: make(chan *slack.MessageEvent, 1),
	}
	client.conversations["bbbb"] = convo
	convo.closed <- struct{}{}

	events := []interface{}{
		&slack.LatencyReport{},
		&slack.RTMError{},
		&slack.InvalidAuthEvent{},
		&slack.MessageEvent{
			Msg: slack.Msg{
				Channel: "bbbb",
				Text:    "text",
			},
		},
	}

	for _, e := range events {
		mock.events <- slack.RTMEvent{Data: e}
	}

	select {
	case msg := <-convo.messages:
		require.Equal("text", msg.Text)
	case <-time.After(100 * time.Millisecond):
		require.FailNow("didn't get the message")
	}
}

func TestHandleRTMEventOpenConvo(t *testing.T) {
	require := require.New(t)
	mock := &slackRTMMock{
		events: make(chan slack.RTMEvent),
	}

	client := newBotClient(
		"aaaa",
		mock,
		NewClient("", ClientOptions{Debug: true}).(*slackClient),
	)
	defer client.stop()

	convo := &botConversation{
		actions:  make(chan slack.AttachmentActionCallback, 1),
		shutdown: make(chan struct{}, 1),
		closed:   make(chan struct{}, 1),
		messages: make(chan *slack.MessageEvent, 1),
	}
	client.conversations["bbbb"] = convo
	convo.closed <- struct{}{}

	mock.events <- slack.RTMEvent{
		Data: &slack.MessageEvent{
			Msg: slack.Msg{
				Channel: "aaaa",
				Text:    "text",
			},
		},
	}

	<-time.After(50 * time.Millisecond)
	client.Lock()
	require.Equal(2, len(client.conversations))
	client.Unlock()
}

func TestHandleRTMEventInitMessage(t *testing.T) {
	require := require.New(t)
	rtm, helloCont, _, botCli := getSlackMocks()
	defer botCli.stop()

	sendRTMMessageEvent(rtm, existentChannel)
	require.Equal(1, openedConversationsCount(botCli))
	require.Equal(0, calledIntroCount(helloCont))

	sendRTMMessageEvent(rtm, "notExistentChannel")
	require.Equal(2, openedConversationsCount(botCli))
	require.Equal(1, calledIntroCount(helloCont))
}

func TestHandleIMCreatedEvent(t *testing.T) {
	require := require.New(t)
	mock := &slackRTMMock{
		events: make(chan slack.RTMEvent),
	}

	ctrl := &helloCtrl{}
	cli := NewClient("", ClientOptions{Debug: true}).(*slackClient)
	cli.SetIntroHandler(ctrl)

	client := newBotClient(
		"aaaa",
		mock,
		cli,
	)
	defer client.stop()

	mock.events <- slack.RTMEvent{
		Data: &slack.IMCreatedEvent{
			Channel: slack.ChannelCreatedInfo{
				ID: "D345345",
			},
		},
	}

	<-time.After(50 * time.Millisecond)
	client.Lock()
	require.Equal(1, len(client.conversations))
	client.Unlock()
	ctrl.Lock()
	require.Equal(1, ctrl.calledIntro)
	ctrl.Unlock()
}

func TestHandleGroupJoinedEvent(t *testing.T) {
	require := require.New(t)
	mock := &slackRTMMock{
		events: make(chan slack.RTMEvent),
	}

	ctrl := &helloCtrl{}
	cli := NewClient("", ClientOptions{Debug: true}).(*slackClient)
	cli.SetIntroHandler(ctrl)

	client := newBotClient(
		"aaaa",
		mock,
		cli,
	)
	defer client.stop()

	ev := slack.RTMEvent{Data: &slack.GroupJoinedEvent{}}
	ev.Data.(*slack.GroupJoinedEvent).Channel.ID = "G394820"
	mock.events <- ev

	<-time.After(50 * time.Millisecond)
	client.Lock()
	require.Equal(1, len(client.conversations))
	client.Unlock()
	ctrl.Lock()
	require.Equal(1, ctrl.calledIntro)
	ctrl.Unlock()
}

func TestHandleJob(t *testing.T) {
	client := &botClient{
		conversations: make(map[string]*botConversation),
	}

	client.conversations["bbbb"] = &botConversation{}
	client.conversations["aaaa"] = &botConversation{}

	var executed int32
	client.handleJob(func(_ flamingo.Bot, _ flamingo.Channel) error {
		atomic.AddInt32(&executed, 1)
		return nil
	})

	client.handleJob(func(_ flamingo.Bot, _ flamingo.Channel) error {
		atomic.AddInt32(&executed, 1)
		return errors.New("foo")
	})

	require.Equal(t, int32(4), atomic.LoadInt32(&executed))
}

func getSlackMocks() (*slackRTMMock, *helloCtrl, *slackClient, *botClient) {
	slackRTM := &slackRTMMock{
		events: make(chan slack.RTMEvent),
	}

	helloController := &helloCtrl{}
	cli := NewClient("", ClientOptions{Debug: true}).(*slackClient)
	cli.SetIntroHandler(helloController)

	clientBot := newBotClient(
		"aaaa",
		slackRTM,
		cli,
	)

	conv, _, _ := clientBot.newConversation("existentChannel")
	conv.closed <- struct{}{}

	return slackRTM, helloController, cli, clientBot
}

func sendRTMMessageEvent(rtm *slackRTMMock, channel string) {
	rtm.events <- slack.RTMEvent{
		Data: &slack.MessageEvent{
			Msg: slack.Msg{
				Channel: channel,
				Text:    "text",
			},
		},
	}
}

func openedConversationsCount(botCli *botClient) int {
	<-time.After(50 * time.Millisecond)
	botCli.Lock()
	number := len(botCli.conversations)
	botCli.Unlock()
	return number
}

func calledIntroCount(helloCont *helloCtrl) int {
	helloCont.Lock()
	number := helloCont.calledIntro
	helloCont.Unlock()
	return number
}
