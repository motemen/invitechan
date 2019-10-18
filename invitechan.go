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
		verifyEv := ev.InnerEvent.Data.(*slackevents.EventsAPIURLVerificationEvent)
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
		if _, ok := hasPrefix(msgEv.Text, "list"); ok {
			channels, err := getOpenChannels(ctx)
			if err != nil {
				mustReply(ctx, msgEv, fmt.Sprintf("Error: %s", err))
				return
			}

			msg := "Available channels:\n"
			for chName := range channels {
				msg += "• " + chName + "\n"
			}
			msg += `Tell me “join _channel_” to join one!`

			mustReply(ctx, msgEv, msg)
		} else if chName, ok := hasPrefix(msgEv.Text, "join "); ok {
			channels, err := getOpenChannels(ctx)
			if err != nil {
				mustReply(ctx, msgEv, fmt.Sprintf("Error: %s", err))
				return
			}

			ch, ok := channels[chName]
			if !ok {
				mustReply(ctx, msgEv, fmt.Sprintf("Sorry, channel #%s is not open to multi-channel guests.", chName))
				return
			}

			mustReply(ctx, msgEv, fmt.Sprintf("Okay, I will invite you to #%s!", chName))

			_, err = userClient.InviteUserToChannelContext(ctx, ch.ID, msgEv.User)
			if err != nil {
				mustReply(ctx, msgEv, fmt.Sprintf("Error: %s", err))
				return
			}
		} else if chName, ok := hasPrefix(msgEv.Text, "leave "); ok {
			channels, err := getOpenChannels(ctx)
			if err != nil {
				mustReply(ctx, msgEv, fmt.Sprintf("Error: %s", err))
				return
			}

			ch, ok := channels[chName]
			if !ok {
				mustReply(ctx, msgEv, fmt.Sprintf("Sorry, channel #%s is not open to multi-channel guests.", chName))
				return
			}

			mustReply(ctx, msgEv, fmt.Sprintf("Okay, I will kick you from #%s!", chName))

			err = userClient.KickUserFromConversationContext(ctx, ch.ID, msgEv.User)
			if err != nil {
				mustReply(ctx, msgEv, fmt.Sprintf("Error: %s", err))
				return
			}
		} else {
			msg := `Hello! With me multi-channel guests can join open channels freely.

*If you are a multi-channel guest:*
Tell me:
• “list” to list open channels
• “join _channel_” to join one
• “leave _channel_” to leave one

*If you are a regular user:*
Public channels where I’m in are marked open to guests.
Invite me to channels so that guests can join them.
`
			mustReply(ctx, msgEv, msg)
		}
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

func mustReply(ctx context.Context, msgEv *slackevents.MessageEvent, text string) {
	_, _, err := botClient.PostMessageContext(ctx, msgEv.Channel, slack.MsgOptionText(text, false))
	if err != nil {
		panic(err)
	}
}
