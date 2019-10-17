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

const usageText = `Hello! It's invitechan.
You can use **/plzinviteme** slash command to ...
`

// doSlashCommand handles /plzinviteme slash command.
// /plzinviteme [channel <channel>]
func doSlashCommand(w http.ResponseWriter, req *http.Request) {
	text := req.FormValue("text")
	user := req.FormValue("user_id")

	if strings.HasPrefix(text, "channel ") {
		chName := text[len("channel "):]

		channels, err := getInvitableChannels(req.Context(), chName)
		if err != nil {
			panic(err)
		}

		ch := channels[chName]
		_, err = userClient.InviteUserToChannelContext(req.Context(), ch.ID, user)
		if err != nil {
			panic(err)
		}

		w.WriteHeader(200)
	} else {
		msg := "Channels:\n"
		channels, err := getInvitableChannels(req.Context(), "")
		if err != nil {
			panic(err)
		}

		for _, ch := range channels {
			msg += "* " + ch.Name + "\n"
		}

		responseURL := req.FormValue("response_url")
		_, err = botClient.PostEphemeralContext(
			req.Context(),
			req.FormValue("channel_id"),
			req.FormValue("user_id"),
			slack.MsgOptionText(msg, false),
			slack.MsgOptionResponseURL(responseURL, ""),
		)
		if err != nil {
			panic(err)
		}
	}
}

func Do(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	if req.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		// Must be slack command. Handle /plzinviteme
		doSlashCommand(w, req)
		return
	}

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
	// - @invitechan list
	// - @invitechan join <channel>
	// - @invitechan leave <channel>
	if msgEv.Type == slack.TYPE_MESSAGE && msgEv.SubType == "" {
		if _, ok := hasPrefix(msgEv.Text, "list"); ok {
			channels, err := getInvitableChannels(ctx, "")
			if err != nil {
				panic(err)
			}
			msg := "Available channels:\n"
			for chName := range channels {
				msg += "• " + chName + "\n"
			}
			msg += `Tell me “join _channel_” to join one!`
			_, _, err = botClient.PostMessageContext(ctx, msgEv.Channel, slack.MsgOptionText(msg, false))
			if err != nil {
				panic(err)
			}
		} else if chName, ok := hasPrefix(msgEv.Text, "join "); ok {
			channels, err := getInvitableChannels(ctx, chName)
			if err != nil {
				panic(err)
			}
			ch := channels[chName]
			_, err = userClient.InviteUserToChannelContext(ctx, ch.ID, msgEv.User)
			if err != nil {
				panic(err)
			}
		} else if chName, ok := hasPrefix(msgEv.Text, "leave "); ok {
			channels, err := getInvitableChannels(ctx, chName)
			if err != nil {
				panic(err)
			}
			ch := channels[chName]
			err = userClient.KickUserFromConversationContext(ctx, ch.ID, msgEv.User)
			if err != nil {
				panic(err)
			}
		} else {
			msg := `Hello! I let multi-channel guests to freely join open channels.

*If you are a multi-channel guest:*
Tell me:
• “list”
• “join _channel_”
• “leave _channel_”

*If you are a regular user:*
Public channels where I’m in are marked open to guests.
Invite me to channels so that guests can join them.
`
			botClient.PostMessageContext(ctx, msgEv.Channel, slack.MsgOptionText(msg, false))
		}
	}
}

func hasPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// We don't mind caching channels list forever, as we assume serverless nature.
var cachedChannels map[string]slack.Channel

// TODO: fix race
func getInvitableChannels(ctx context.Context, hintChannelName string) (map[string]slack.Channel, error) {
	if cachedChannels != nil {
		return cachedChannels, nil
	}

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
			log.Printf("%#v", ch)
			channels[ch.Name] = ch
			if hintChannelName != "" && ch.Name == hintChannelName {
				// Early return
				return channels, nil
			}
		}

		if cursor == "" {
			break
		}
		params.Cursor = cursor
	}

	cachedChannels = channels
	return channels, nil
}
