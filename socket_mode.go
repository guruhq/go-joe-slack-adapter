package slack

import (
	"context"
	"fmt"
	"os"

	"github.com/go-joe/joe"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"go.uber.org/zap"
)

// SocketModeServer is an adapter that receives messages from Slack using socket mode.
//
// See https://api.slack.com/apis/connections/socket
type SocketModeServer struct {
	*BotAdapter
	SocketMode *socketmode.Client
	conf       SocketModeConfig
	opts       []slackevents.Option
}

// SocketModeAdapter returns a new SocketModeServer as joe.Module.
// If you want to use the slack RTM API instead (i.e. using web sockets), you
// should use the slack.Adapter(…) function instead.
// If you want to use the EventsAPI instead (receiving events through HTTPS), you
// should use the slack.EventsAPIAdapter(...) function instead.
func SocketModeAdapter(token, appToken, verificationToken string, opts ...Option) joe.Module {
	return joe.ModuleFunc(func(joeConf *joe.Config) error {
		conf, err := newConf(token, joeConf, opts)
		if err != nil {
			return err
		}
		conf.VerificationToken = verificationToken
		conf.AppToken = appToken

		a, err := NewSocketModeServer(joeConf.Context, conf)
		if err != nil {
			return err
		}

		a.logger.Info("SOCKET MODE!")

		joeConf.SetAdapter(a)
		return nil
	})
}

// NewSocketModeServer creates a new *SocketModeServer that connects to Slack
// using socket mode (websockets). Note that you will usually configure this type of slack
// adapter as joe.Module (i.e. using the SocketModeAdapter function of this package).
func NewSocketModeServer(ctx context.Context, conf Config) (*SocketModeServer, error) {
	events := make(chan slackEvent)
	client := slack.New(conf.Token, conf.slackOptions()...)
	adapter, err := newAdapter(ctx, client, nil, events, conf)
	if err != nil {
		return nil, err
	}

	socketMode := socketmode.New(
		client,
		socketmode.OptionDebug(true),
		socketmode.OptionLog(zap.NewStdLog(conf.Logger)))

	_, err = client.AuthTest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "SLACK_BOT_TOKEN is invalid: %v\n", err)
		os.Exit(1)
	}

	a := &SocketModeServer{
		BotAdapter: adapter,
		SocketMode: socketMode,
		conf:       conf.SocketMode,
	}

	a.opts = append(a.opts, slackevents.OptionVerifyToken(
		&slackevents.TokenComparator{
			VerificationToken: conf.VerificationToken,
		},
	))

	//var handler http.Handler = http.HandlerFunc(a.httpHandler)
	//if conf.EventsAPI.Middleware != nil {
	//	handler = conf.EventsAPI.Middleware(handler)
	//}
	//
	//a.http = &http.Server{
	//	Addr:         listenAddr,
	//	Handler:      handler,
	//	ErrorLog:     zap.NewStdLog(conf.Logger),
	//	TLSConfig:    conf.EventsAPI.TLSConf,
	//	ReadTimeout:  conf.EventsAPI.ReadTimeout,
	//	WriteTimeout: conf.EventsAPI.WriteTimeout,
	//}

	return a, nil
}

// RegisterAt implements the joe.Adapter interface by emitting the slack API
// events to the given brain.
func (a *SocketModeServer) RegisterAt(brain *joe.Brain) {
	// Start the event listener server. The goroutine will stop when the adapter is closed.
	go a.startEventListener()
	a.BotAdapter.RegisterAt(brain)
}

// startEventListener starts listening for events through Slack's
// socket-mode client.
func (a *SocketModeServer) startEventListener() {
	for envelope := range a.SocketMode.Events {
		switch envelope.Type {
		case socketmode.EventTypeEventsAPI:
			// Events API:

			// Acknowledge the eventPayload first
			a.SocketMode.Ack(*envelope.Request)

			eventPayload, _ := envelope.Data.(slackevents.EventsAPIEvent)
			a.handleEvent(eventPayload.InnerEvent)
		//case socketmode.EventTypeInteractive:
		//	// Shortcuts:
		//
		//	payload, _ := envelope.Data.(slack.InteractionCallback)
		//	switch payload.Type {
		//	case slack.InteractionTypeShortcut:
		//		if payload.CallbackID == "socket-mode-shortcut" {
		//			socketMode.Ack(*envelope.Request)
		//			modalView := slack.ModalViewRequest{
		//				Type:       "modal",
		//				CallbackID: "modal-id",
		//				Title: slack.NewTextBlockObject(
		//					"plain_text",
		//					"New Task",
		//					false,
		//					false,
		//				),
		//				Submit: slack.NewTextBlockObject(
		//					"plain_text",
		//					"Submit",
		//					false,
		//					false,
		//				),
		//				Close: slack.NewTextBlockObject(
		//					"plain_text",
		//					"Cancel",
		//					false,
		//					false,
		//				),
		//				Blocks: slack.Blocks{
		//					BlockSet: []slack.Block{
		//						slack.NewInputBlock(
		//							"input-task",
		//							slack.NewTextBlockObject(
		//								"plain_text",
		//								"Task Description",
		//								false,
		//								false,
		//							),
		//							// multiline is not yet supported
		//							slack.NewPlainTextInputBlockElement(
		//								slack.NewTextBlockObject(
		//									"plain_text",
		//									"Describe the task in detail with its timeline",
		//									false,
		//									false,
		//								),
		//								"input",
		//							),
		//						),
		//					},
		//				},
		//			}
		//			resp, err := webApi.OpenView(payload.TriggerID, modalView)
		//			if err != nil {
		//				log.Printf("Failed to opemn a modal: %v", err)
		//			}
		//			socketMode.Debugf("views.open response: %v", resp)
		//		}
		//	case slack.InteractionTypeViewSubmission:
		//		// View Submission:
		//		if payload.CallbackID == "modal-id" {
		//			socketMode.Debugf("Submitted Data: %v", payload.View.State.Values)
		//			socketMode.Ack(*envelope.Request)
		//		}
		//	default:
		//		// Others
		//		socketMode.Debugf("Skipped: %v", payload)
		//	}

		default:
			a.SocketMode.Debugf("Skipped: %v", envelope.Type)
		}
	}

	err := a.SocketMode.Run()
	if err != nil {
		a.logger.Error("SocketMode runtime failure", zap.Error(err))
	}
}

//func (a *SocketModeServer) startHTTPServer() {
//	var err error
//	if a.conf.CertFile == "" {
//		err = a.http.ListenAndServe()
//	} else {
//		err = a.http.ListenAndServeTLS(a.conf.CertFile, a.conf.KeyFile)
//	}
//
//	if err != nil && err != http.ErrServerClosed {
//		a.logger.Error("HTTP server failure", zap.Error(err))
//	}
//}
//
//func (a *SocketModeServer) httpHandler(w http.ResponseWriter, r *http.Request) {
//	body, err := io.ReadAll(r.Body)
//	if err != nil {
//		a.logger.Error("Failed to read request body", zap.Error(err))
//		w.WriteHeader(http.StatusInternalServerError)
//		return
//	}
//
//	eventsAPIEvent, err := slackevents.ParseEvent(body, a.opts...)
//	if err != nil {
//		a.logger.Error("Failed to parse slack event", zap.Error(err))
//		w.WriteHeader(http.StatusInternalServerError)
//		return
//	}
//
//	switch eventsAPIEvent.Type {
//	case slackevents.URLVerification:
//		a.handleURLVerification(body, w)
//
//	case slackevents.CallbackEvent:
//		a.handleEvent(eventsAPIEvent.InnerEvent)
//
//	default:
//		a.logger.Error("Received unknown top level event type",
//			zap.String("type", eventsAPIEvent.Type),
//		)
//	}
//}

//func (a *SocketModeServer) handleURLVerification(req []byte, resp http.ResponseWriter) {
//	a.logger.Info("Received URL verification challenge request")
//
//	var r slackevents.ChallengeResponse
//	err := json.Unmarshal(req, &r)
//	if err != nil {
//		a.logger.Error("Failed to unmarshal challenge as JSON", zap.Error(err))
//		resp.WriteHeader(http.StatusInternalServerError)
//		return
//	}
//
//	resp.Header().Set("Content-Type", "text")
//	_, err = fmt.Fprint(resp, r.Challenge)
//	if err != nil {
//		a.logger.Error("Failed to write challenge response", zap.Error(err))
//	}
//
//	resp.WriteHeader(http.StatusOK)
//}

func (a *SocketModeServer) handleEvent(innerEvent slackevents.EventsAPIInnerEvent) {
	switch ev := innerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		a.handleMessageEvent(ev)

	case *slackevents.AppMentionEvent:
		a.handleAppMentionEvent(ev)

	case *slackevents.ReactionAddedEvent:
		a.handleReactionAddedEvent(ev)

	default:
		if a.logUnknownMessageTypes {
			a.logger.Error("Received unknown event type",
				zap.String("type", innerEvent.Type),
				zap.Any("data", innerEvent.Data),
				zap.String("go_type", fmt.Sprintf("%T", innerEvent.Data)),
			)
		}
	}
}

func (a *SocketModeServer) handleMessageEvent(ev *slackevents.MessageEvent) {
	var edited *slack.Edited
	if ev.Edited != nil {
		edited = &slack.Edited{
			User:      ev.Edited.User,
			Timestamp: ev.Edited.TimeStamp,
		}
	}

	icons := &slack.Icon{}
	if ev.Icons != nil {
		icons = &slack.Icon{
			IconURL:   ev.Icons.IconURL,
			IconEmoji: ev.Icons.IconEmoji,
		}
	}

	a.events <- slackEvent{
		Type: ev.Type,
		Data: &slack.MessageEvent{
			Msg: slack.Msg{
				Type:            ev.Type,
				Channel:         ev.Channel,
				User:            ev.User,
				Text:            ev.Text,
				Timestamp:       ev.TimeStamp,
				ThreadTimestamp: ev.ThreadTimeStamp,
				Edited:          edited,
				SubType:         ev.SubType,
				EventTimestamp:  ev.EventTimeStamp.String(),
				BotID:           ev.BotID,
				Username:        ev.Username,
				Icons:           icons,
			},
		},
	}
}

func (a *SocketModeServer) handleAppMentionEvent(ev *slackevents.AppMentionEvent) {
	a.events <- slackEvent{
		Type: ev.Type,
		Data: &slack.MessageEvent{
			Msg: slack.Msg{
				Type:            ev.Type,
				User:            ev.User,
				Text:            ev.Text,
				Timestamp:       ev.TimeStamp,
				ThreadTimestamp: ev.ThreadTimeStamp,
				Channel:         ev.Channel,
				EventTimestamp:  ev.EventTimeStamp.String(),
				BotID:           ev.BotID,
			},
		},
	}
}

func (a *SocketModeServer) handleReactionAddedEvent(ev *slackevents.ReactionAddedEvent) {
	evt := &slack.ReactionAddedEvent{
		Type:           ev.Type,
		User:           ev.User,
		ItemUser:       ev.ItemUser,
		Reaction:       ev.Reaction,
		EventTimestamp: ev.EventTimestamp,
	}

	evt.Item.Type = ev.Item.Type
	evt.Item.Channel = ev.Item.Channel
	evt.Item.Timestamp = ev.Item.Timestamp

	a.events <- slackEvent{
		Type: ev.Type,
		Data: evt,
	}
}

// Close shuts down the disconnects the adapter from the slack API.
func (a *SocketModeServer) Close() error {
	ctx := context.Background()
	if a.conf.ShutdownTimeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, a.conf.ShutdownTimeout)
		defer cancel()
	}

	// After we are sure we do not get any new events from the HTTP server, we
	// must stop event processing loop by closing the channel.
	close(a.events)

	return nil
}
