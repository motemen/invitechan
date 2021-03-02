package invitechan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"cloud.google.com/go/datastore"
	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackevents"
	"golang.org/x/oauth2"
)

// https://api.slack.com/authentication/oauth-v2

var slackOAuthV2Endpoint = oauth2.Endpoint{
	AuthURL:  "https://slack.com/oauth/v2/authorize",
	TokenURL: "https://slack.com/api/oauth.v2.access",
}

var fixedTokens = teamTokens{
	UserToken: os.Getenv("SLACK_TOKEN_USER"),
	BotToken:  os.Getenv("SLACK_TOKEN_BOT"),
}

var datastoreClient *datastore.Client

const datastoreKindTeamTokens = "teamTokens"

type teamTokens struct {
	UserToken string
	BotToken  string
}

func (t teamTokens) Valid() bool {
	return len(t.UserToken) > 0 && len(t.BotToken) > 0
}

func init() {
	var err error
	// FIXME(motemen): as of go113, GCP_PROJECT environment variables is not automatically set, so users must set it manually (in env.{project}.yaml)
	datastoreClient, err = datastore.NewClient(context.Background(), os.Getenv("GCP_PROJECT"))
	if err != nil {
		panic(err)
	}
}

type messageContext struct {
	text      string
	userID    string
	channelID string
	teamID    string
	opts      []slack.MsgOption
}

func (msg *messageContext) botClient(ctx context.Context) (*slack.Client, error) {
	tokens := fixedTokens
	if !tokens.Valid() {
		key := datastore.NameKey(datastoreKindTeamTokens, msg.teamID, nil)
		err := datastoreClient.Get(ctx, key, &tokens)
		if err != nil {
			return nil, err
		}
	}

	return slack.New(tokens.BotToken), nil
}

func (msg *messageContext) userClient(ctx context.Context) (*slack.Client, error) {
	tokens := fixedTokens
	if !tokens.Valid() {
		key := datastore.NameKey(datastoreKindTeamTokens, msg.teamID, nil)
		err := datastoreClient.Get(ctx, key, &tokens)
		if err != nil {
			return nil, err
		}
	}

	return slack.New(tokens.UserToken), nil
}

var mux *http.ServeMux

func init() {
	mux = http.NewServeMux()
	mux.HandleFunc("/command", serveCommand)
	mux.HandleFunc("/events", serveEvents)
	mux.HandleFunc("/auth", serveAuth)
	mux.HandleFunc("/auth/callback", serveAuthCallback)
}

// Do is the Cloud Functions endpoint.
func Do(w http.ResponseWriter, req *http.Request) {
	mux.ServeHTTP(w, req)
}

// https://api.slack.com/interactivity/slash-commands
func serveCommand(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	text := req.FormValue("text")
	teamID := req.FormValue("team_id")
	userID := req.FormValue("user_id")
	channelID := req.FormValue("channel_id")
	responseURL := req.FormValue("response_url")

	handleCommand(ctx, messageContext{
		userID:    userID,
		channelID: channelID,
		teamID:    teamID,
		text:      text,
		opts: []slack.MsgOption{
			slack.MsgOptionResponseURL(responseURL, "ephemeral"),
		},
	},
	)
}

func serveEvents(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	v, err := slack.NewSecretsVerifier(req.Header, os.Getenv("SLACK_APP_SIGNING_SECRET"))
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	defer req.Body.Close()
	buf, err := ioutil.ReadAll(io.TeeReader(req.Body, &v))
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := v.Ensure(); err != nil {
		log.Println(err)
		http.Error(w, "Verification failed", http.StatusUnauthorized)
		return
	}

	ev, err := slackevents.ParseEvent(json.RawMessage(buf), slackevents.OptionNoVerifyToken())
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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
			userID:    msgEv.User,
			channelID: msgEv.Channel,
			teamID:    ev.TeamID,
			text:      msgEv.Text,
		})
	}
}

var oauth2Config = &oauth2.Config{
	ClientID:     os.Getenv("SLACK_APP_CLIENT_ID"),
	ClientSecret: os.Getenv("SLACK_APP_CLIENT_SECRET"),
	Scopes:       []string{"commands", "channels:read"}, // NOTE: bot scope is not needed for V2 OAuth
	Endpoint:     slackOAuthV2Endpoint,
	RedirectURL: fmt.Sprintf(
		"https://%s-%s.cloudfunctions.net/%s/auth/callback",
		os.Getenv("FUNCTION_REGION"),
		os.Getenv("GCP_PROJECT"),
		os.Getenv("FUNCTION_NAME"),
	),
}

// https://api.slack.com/docs/oauth
func serveAuth(w http.ResponseWriter, req *http.Request) {
	u := oauth2Config.AuthCodeURL("", oauth2.SetAuthURLParam("user_scope", "channels:write")) // TODO: state
	http.Redirect(w, req, u, http.StatusFound)
}

func serveAuthCallback(w http.ResponseWriter, req *http.Request) {
	if err := req.URL.Query().Get("error"); err != "" {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, err)
		return
	}

	ctx := req.Context()

	// https://api.slack.com/methods/oauth.v2.access
	// TODO: state
	token, err := oauth2Config.Exchange(req.Context(), req.URL.Query().Get("code"))
	if err != nil {
		log.Printf("oauth2Config.Exchange: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	team := token.Extra("team").(map[string]interface{})
	authedUser := token.Extra("authed_user").(map[string]interface{})
	log.Printf("AuthCallback: %#v; team=%v authed_user=%v", token, team, authedUser)
	tokens := teamTokens{
		BotToken:  token.AccessToken,
		UserToken: authedUser["access_token"].(string),
	}

	key := datastore.NameKey(datastoreKindTeamTokens, team["id"].(string), nil)
	_, err = datastoreClient.Put(ctx, key, &tokens)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Fprintln(w, "<p>@invitechan has been installed! Use <code>/plzinviteme</code> to use it.</p>")
}

func handleCommand(ctx context.Context, msg messageContext) {
	if _, ok := hasPrefix(msg.text, "list"); ok {
		channels, err := getOpenChannels(ctx, msg)
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
		channels, err := getOpenChannels(ctx, msg)
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

		userClient, err := msg.userClient(ctx)
		if err != nil {
			log.Fatal(err)
		}
		_, err = userClient.InviteUserToChannelContext(ctx, ch.ID, msg.userID)
		if err != nil {
			mustReply(ctx, msg, fmt.Sprintf("Error: %s", err))
			return
		}
	} else if chName, ok := hasPrefix(msg.text, "leave "); ok {
		channels, err := getOpenChannels(ctx, msg)
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

		userClient, err := msg.userClient(ctx)
		if err != nil {
			log.Fatal(err)
		}
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

func getOpenChannels(ctx context.Context, msg messageContext) (map[string]slack.Channel, error) {
	channels := map[string]slack.Channel{}
	params := &slack.GetConversationsForUserParameters{
		Types:           []string{"public_channel"},
		ExcludeArchived: true,
	}
	botClient, err := msg.botClient(ctx)
	if err != nil {
		return nil, err
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
	botClient, err := msg.botClient(ctx)
	if err != nil {
		panic(err)
	}
	opts := append(msg.opts, slack.MsgOptionText(text, false))
	_, _, err = botClient.PostMessageContext(ctx, msg.channelID, opts...)
	if err != nil {
		panic(err)
	}
}
