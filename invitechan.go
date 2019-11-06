package invitechan

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackevents"
)

var (
	clientTokenUser = mustGetEnv("SLACK_TOKEN_USER")
	clientTokenBot  = mustGetEnv("SLACK_TOKEN_BOT")
)

type messageContext struct {
	text      string
	channelID string
	userID    string
	opts      []slack.MsgOption
}

func mustGetEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		panic(fmt.Sprintf("$%s must be set", name))
	}
	return v
}

var (
	botClient  = slack.New(clientTokenBot, slack.OptionDebug(true))
	userClient = slack.New(clientTokenUser, slack.OptionDebug(true))
)

func Command(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	text := req.FormValue("text")
	userID := req.FormValue("user_id")
	channelID := req.FormValue("channel_id")
	responseURL := req.FormValue("response_url")

	log.Println(text, userID, channelID, responseURL)

	handleCommand(ctx, messageContext{
		channelID: channelID,
		userID:    userID,
		text:      text,
		opts: []slack.MsgOption{
			slack.MsgOptionResponseURL(responseURL, "ephemeral"),
		},
	},
	)
}

func Do(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	defer req.Body.Close()
	buf, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	ev, err := slackevents.ParseEvent(json.RawMessage(buf), slackevents.OptionNoVerifyToken())
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	if ev.Type == slackevents.URLVerification {
		verifyEv := ev.Data.(*slackevents.EventsAPIURLVerificationEvent)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, verifyEv.Challenge)
		return
	}

	if ev.Type != slackevents.CallbackEvent {
		log.Printf("Unknown event type: %q", ev.Type)
	}

	msgEv := ev.InnerEvent.Data.(*slackevents.MessageEvent)
	log.Printf("%#v", msgEv)

	// DM commands:
	// - list
	// - join <channel>
	// - leave <channel>
	if msgEv.Type == slack.TYPE_MESSAGE && msgEv.SubType == "" {
		handleCommand(ctx, messageContext{
			channelID: msgEv.Channel,
			userID:    msgEv.User,
			text:      msgEv.Text,
		})
	}
}

func handleCommand(ctx context.Context, msg messageContext) {
	if _, ok := hasPrefix(msg.text, "list"); ok {
		channels, err := getOpenChannels(ctx)
		if err != nil {
			mustReply(ctx, msg, fmt.Sprintf("Error: %s", err))
			return
		}

		text := "Available channels:\n"
		for chName := range channels {
			text += "• " + chName + "\n"
		}
		text += `Tell me “join _channel_” to join one!`

		mustReply(ctx, msg, text)
	} else if chName, ok := hasPrefix(msg.text, "join "); ok {
		channels, err := getOpenChannels(ctx)
		if err != nil {
			mustReply(ctx, msg, fmt.Sprintf("Error: %s", err))
			return
		}

		ch, ok := channels[chName]
		if !ok {
			mustReply(ctx, msg, fmt.Sprintf("Sorry, channel #%s is not open to multi-channel guests.", chName))
			return
		}

		mustReply(ctx, msg, fmt.Sprintf("Okay, I will invite you to #%s!", chName))

		_, err = userClient.InviteUserToChannelContext(ctx, ch.ID, msg.userID)
		if err != nil {
			mustReply(ctx, msg, fmt.Sprintf("Error: %s", err))
			return
		}
	} else if chName, ok := hasPrefix(msg.text, "leave "); ok {
		channels, err := getOpenChannels(ctx)
		if err != nil {
			mustReply(ctx, msg, fmt.Sprintf("Error: %s", err))
			return
		}

		ch, ok := channels[chName]
		if !ok {
			mustReply(ctx, msg, fmt.Sprintf("Sorry, channel #%s is not open to multi-channel guests.", chName))
			return
		}

		mustReply(ctx, msg, fmt.Sprintf("Okay, I will kick you from #%s!", chName))

		err = userClient.KickUserFromConversationContext(ctx, ch.ID, msg.userID)
		if err != nil {
			mustReply(ctx, msg, fmt.Sprintf("Error: %s", err))
			return
		}
	} else {
		text := `Hello! With me multi-channel guests can join open channels freely.

*If you are a multi-channel guest:*
Tell me:
• “list” to list open channels
• “join _channel_” to join one
• “leave _channel_” to leave one

*If you are a regular user:*
Public channels where I’m in are marked open to guests.
Invite me to channels so that guests can join them.
`
		mustReply(ctx, msg, text)
	}
}

func hasPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

func getOpenChannels(ctx context.Context) (map[string]slack.Channel, error) {
	channels := map[string]slack.Channel{}
	params := &slack.GetConversationsForUserParameters{
		Types:           []string{"public_channel"},
		ExcludeArchived: true,
	}
	for {
		chs, cursor, err := botClient.GetConversationsForUserContext(ctx, params)
		if err != nil {
			return nil, err
		}

		for _, ch := range chs {
			channels[ch.Name] = ch
		}

		if cursor == "" {
			break
		}
		params.Cursor = cursor
	}

	return channels, nil
}

func mustReply(ctx context.Context, msg messageContext, text string) {
	opts := append(msg.opts, slack.MsgOptionText(text, false))
	_, _, err := botClient.PostMessageContext(ctx, msg.channelID, opts...)
	if err != nil {
		panic(err)
	}
}
