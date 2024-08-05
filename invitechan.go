package invitechan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"

	"cloud.google.com/go/datastore"
	"github.com/blendle/zapdriver"
	"github.com/hashicorp/go-cleanhttp"
	"github.com/motemen/go-nuts/httputil"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"go.uber.org/zap"
)

// https://api.slack.com/authentication/oauth-v2

var slackOAuthV2Endpoint = oauth2.Endpoint{
	AuthURL:  "https://slack.com/oauth/v2/authorize",
	TokenURL: "https://slack.com/api/oauth.v2.access",
}

var fixedTokens = teamTokens{
	// UserToken: os.Getenv("SLACK_TOKEN_USER"),
	BotToken: os.Getenv("SLACK_TOKEN_BOT"),
}

var datastoreClient *datastore.Client

const datastoreKindTeamTokens = "teamTokens"

type teamTokens struct {
	// UserToken string
	BotToken string
}

func (t teamTokens) Valid() bool {
	return /* len(t.UserToken) > 0 && */ len(t.BotToken) > 0
}

var logger *zap.Logger

func init() {
	var err error
	// FIXME(motemen): as of go113, GCP_PROJECT environment variables is not automatically set, so users must set it manually (in env.{project}.yaml)
	datastoreClient, err = datastore.NewClient(context.Background(), os.Getenv("GCP_PROJECT"))
	if err != nil {
		panic(err)
	}

	logger, err = zapdriver.NewDevelopment()
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

type slackClientKind int

const (
	slackClientTypeBot slackClientKind = iota
	slackClientTypeUser
)

func buildSlackClient(ctx context.Context, teamID string, kind slackClientKind) (*slack.Client, error) {
	tokens := fixedTokens
	if !tokens.Valid() {
		key := datastore.NameKey(datastoreKindTeamTokens, teamID, nil)
		err := datastoreClient.Get(ctx, key, &tokens)
		if err != nil {
			return nil, err
		}
	}

	switch kind {
	case slackClientTypeBot:
		return slack.New(tokens.BotToken), nil

	/*
		case slackClientTypeUser:
			return slack.New(tokens.UserToken), nil
	*/

	default:
		panic("")
	}
}

// TODO(motemen): deprecate for buildClient
func (msg *messageContext) botClient(ctx context.Context) (*slack.Client, error) {
	tokens := fixedTokens
	if !tokens.Valid() {
		key := datastore.NameKey(datastoreKindTeamTokens, msg.teamID, nil)
		err := datastoreClient.Get(ctx, key, &tokens)
		if err != nil {
			return nil, err
		}
	}

	return slack.New(tokens.BotToken, slack.OptionDebug(true)), nil
}

// TODO(motemen): deprecate for buildClient
/*
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
*/

var mux *http.ServeMux

func init() {
	mux = http.NewServeMux()
	mux.HandleFunc("/command", serveCommand) // TODO(motemen): deprecate
	mux.HandleFunc("/events", serveEvents)
	mux.HandleFunc("/interactive", serveInteractive)
	mux.HandleFunc("/auth", serveAuth)
	mux.HandleFunc("/auth/callback", serveAuthCallback)
}

// Do is the Cloud Functions endpoint.
func Do(w http.ResponseWriter, req *http.Request) {
	mux.ServeHTTP(w, req)
}

func serveInteractive(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	payload := req.FormValue("payload")

	var callback slack.InteractionCallback
	err := json.Unmarshal([]byte(payload), &callback)
	if err != nil {
		logger.Error("decoding payload", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	action := callback.ActionCallback.BlockActions[0]

	if action.ActionID == "accept" {
		// invite
		msg := messageContext{
			userID:    callback.User.ID,
			channelID: callback.Channel.ID,
			teamID:    callback.Team.ID,
		}
		cli, err := msg.botClient(ctx)
		if err != nil {
			logger.Error("building user client", zap.Error(err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		_, _, _, err = cli.SendMessageContext(
			ctx,
			callback.Channel.ID,
			slack.MsgOptionReplaceOriginal(callback.ResponseURL),
			slack.MsgOptionBlocks(
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("<@%s> wants to join this channel", action.Value), false, false),
					nil, nil,
				),
			),
			slack.MsgOptionAttachments(
				slack.Attachment{
					Color: "#2ab279",
					Blocks: slack.Blocks{
						BlockSet: []slack.Block{
							slack.NewSectionBlock(
								slack.NewTextBlockObject("mrkdwn", fmt.Sprintf(":white_check_mark: <@%s> has accepted", callback.User.ID), false, false),
								nil, nil,
							),
						},
					},
				},
			),
		)
		if err != nil {
			logger.Error("SendMessageContext", zap.Error(err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		_, err = cli.InviteUsersToConversationContext(ctx, callback.Channel.ID, action.Value)
		if err != nil {
			logger.Error("InviteUsersToConversationContext", zap.Error(err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	} else if action.ActionID == "decline" {
		// TODO
	}
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
	})
}

func serveEvents(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	v, err := slack.NewSecretsVerifier(req.Header, os.Getenv("SLACK_APP_SIGNING_SECRET"))
	if err != nil {
		logger.Error("slack.NewSecretsVerifier", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	defer req.Body.Close()
	buf, err := ioutil.ReadAll(io.TeeReader(req.Body, &v))
	if err != nil {
		logger.Error("reading req.Body", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := v.Ensure(); err != nil {
		logger.Error("while verifying", zap.Error(err))
		http.Error(w, "Verification failed", http.StatusUnauthorized)
		return
	}

	ev, err := slackevents.ParseEvent(json.RawMessage(buf), slackevents.OptionNoVerifyToken())
	if err != nil {
		logger.Error("slackevents.ParseEvent", zap.Error(err))
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
		logger.Error(fmt.Sprintf("Unknown event type: %q", ev.Type))
	}

	if msgEv, ok := ev.InnerEvent.Data.(*slackevents.MessageEvent); ok {
		logger.Debug(
			"MessageEvent",
			zap.String("type", msgEv.Type),
			zap.String("subtype", msgEv.SubType),
			zap.String("user", msgEv.User),
			zap.String("channel", msgEv.Channel),
			zap.String("team", ev.TeamID),
			zap.String("text", msgEv.Text),
		)

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
		} else if msgEv.Type == "app_home_opened" {
			/*
				botClient, err := buildSlackClient(ctx, ev.TeamID, slackClientTypeBot)
				botClient.PublishViewContext(ctx, msgEv.User, slack.HomeTabViewRequest{
					Type: "home",
					Blocks: slack.Blocks{
						BlockSet: []slack.Block{},
					},
				}, "")
			*/
		}
	} else if appHomeEv, ok := ev.InnerEvent.Data.(*slackevents.AppHomeOpenedEvent); ok {
		botClient, err := buildSlackClient(ctx, ev.TeamID, slackClientTypeBot)
		if err != nil {
			panic(err)
		}
		botClient.PublishViewContext(ctx, appHomeEv.User, slack.HomeTabViewRequest{
			Type: "home",
			Blocks: slack.Blocks{
				BlockSet: []slack.Block{},
			},
		}, "")
	} else {
		logger.Debug(
			"Unknown MessageEvent",
			zap.ByteString("message", buf),
		)
	}
}

var oauth2Config = &oauth2.Config{
	ClientID:     os.Getenv("SLACK_APP_CLIENT_ID"),
	ClientSecret: os.Getenv("SLACK_APP_CLIENT_SECRET"),
	// Scopes:       []string{"commands", "channels:read"}, // NOTE: bot scope is not needed for V2 OAuth
	Endpoint: slackOAuthV2Endpoint,
	RedirectURL: os.Getenv("FUNCTION_URL_ROOT") + // eg. "https://us-central1-invitechan-public.cloudfunctions.net/invitechan"
		"/auth/callback",
}

// https://api.slack.com/docs/oauth
func serveAuth(w http.ResponseWriter, req *http.Request) {
	u := oauth2Config.AuthCodeURL("") // FIXME:scope
	http.Redirect(w, req, u, http.StatusFound)
}

func serveAuthCallback(w http.ResponseWriter, req *http.Request) {
	if err := req.URL.Query().Get("error"); err != "" {
		w.Header().Set("Content-Type", "text/plain")
		logger.Error("serveAuthCallback", zap.String("error", err))
		fmt.Fprintln(w, err)
		return
	}

	httpClient := cleanhttp.DefaultClient()
	httpClient.Transport = httputil.WrapTransport(
		httpClient.Transport,
		func(req *http.Request, base http.RoundTripper) (*http.Response, error) {
			resp, err := base.RoundTrip(req)
			if err != nil {
				return resp, err
			}
			defer resp.Body.Close()
			b, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return resp, err
			}
			logger.Debug("oauth2 response", zap.ByteString("body", b))
			resp.Body = ioutil.NopCloser(bytes.NewBuffer(b))
			return resp, err
		},
	)

	ctx := req.Context()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	// https://api.slack.com/methods/oauth.v2.access
	// TODO: state
	token, err := oauth2Config.Exchange(ctx, req.URL.Query().Get("code"))
	if err != nil {
		fields := []zap.Field{
			zap.Error(err),
		}
		if re, ok := err.(*oauth2.RetrieveError); ok {
			fields = append(fields, zap.ByteString("response_body", re.Body))
		}
		// oauth2: server response missing access_token なぜ
		// {"ok":false,"error":"bad_redirect_uri"}
		logger.Error("oauth2Config.Exchange", fields...)

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	team := token.Extra("team").(map[string]interface{})
	authedUser := token.Extra("authed_user").(map[string]interface{})
	logger.Debug("oauth2 succeeded", zap.String("team", fmt.Sprintf("%+v", team)), zap.String("authed_user", fmt.Sprintf("%+v", authedUser)))
	tokens := teamTokens{
		BotToken: token.AccessToken,
		// UserToken: authedUser["access_token"].(string),
	}

	key := datastore.NameKey(datastoreKindTeamTokens, team["id"].(string), nil)
	_, err = datastoreClient.Put(ctx, key, &tokens)
	if err != nil {
		logger.Error("datastore put failed", zap.Error(err), zap.String("key", key.String()), zap.String("value", fmt.Sprintf("%+v", tokens)))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	fmt.Fprintln(w, "<p>@invitechan has been installed!</p>")
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

		mustReply(ctx, msg, fmt.Sprintf("Okay, I will ask #%s to invite you!", chName))

		botClient, err := msg.botClient(ctx)
		if err != nil {
			logger.Error("building bot client", zap.Error(err))
			return
		}

		_, _, err = botClient.PostMessageContext(
			ctx,
			ch.ID,
			slack.MsgOptionBlocks(
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("<@%s> wants to join this channel", msg.userID), false, false),
					nil, nil,
				),
				slack.NewActionBlock(
					"",
					slack.NewButtonBlockElement("accept", msg.userID, slack.NewTextBlockObject("plain_text", "Accept", false, false)).WithStyle(slack.StylePrimary),
					slack.NewButtonBlockElement("decline", "", slack.NewTextBlockObject("plain_text", "Decline", false, false)).WithStyle(slack.StyleDanger),
				),
			),
		)
		if err != nil {
			logger.Error("PostMessageContext", zap.Error(err))
			return
		}
		// TODO: post a message asking to invite user to the channel

		/*
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
		*/
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

		botClient, err := msg.botClient(ctx)
		if err != nil {
			logger.Fatal("building user client", zap.Error(err))
		}
		err = botClient.KickUserFromConversationContext(ctx, ch.ID, msg.userID)
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
