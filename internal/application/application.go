// Package application wires the Flows, connectors, Dex Worker, Client, and Slack Trigger together.
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/config"
	"github.com/superdurable-apps/dex-newsletter/internal/subscribers"

	github "github.com/superdurable/dex-connectors-library/connectors/github"
	"github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	llmrouter "github.com/superdurable/dex-connectors-library/connectors/superdurable/llm"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

type Options struct {
	Logger             *slog.Logger
	Config             config.Config
	Store              *localconfig.Store
	FlowServiceAddress string
	WorkerBindAddress  string
	WorkerTarget       string
	BlobCacheDirectory string
	// Connector options let tests point providers at local fakes.
	SlackOptions  []slack.Option
	GitHubOptions []github.Option
	LLMOptions    []llmrouter.Option
	GmailOptions  []gmail.Option
	// WithoutSlackTrigger skips the Socket Mode runner, for tests that start runs directly.
	WithoutSlackTrigger bool
}

type Application struct {
	Subscribers *subscribers.Service
	Drafts      *blogpost.EditorService
	BlogPosts   *blogpost.Flow
	Client      *dex.Client
	logger      *slog.Logger
	worker      *dex.Worker
	cache       *blobcache.Cache
	trigger     *slack.MessageTriggerRunner

	workerBindAddress string
}

// subscriberDirectory lets the BlogPost Flow read the list through the Client built after the Registry.
type subscriberDirectory struct{ service *subscribers.Service }

func (directory *subscriberDirectory) ListSubscribers(ctx context.Context) ([]string, error) {
	return directory.service.ListSubscribers(ctx)
}

func New(options Options) (*Application, error) {
	logger := options.Logger
	store := options.Store
	slackConnection, err := slack.NewLocalConnection(store, blogpost.SlackConnectionName, options.SlackOptions...)
	if err != nil {
		return nil, connectionError(store, err)
	}
	githubConnection, err := github.NewLocalConnection(store, blogpost.GitHubConnectionName, options.GitHubOptions...)
	if err != nil {
		return nil, connectionError(store, err)
	}
	llmConnection, err := llmrouter.NewLocalConnection(store, blogpost.LLMConnectionName, options.LLMOptions...)
	if err != nil {
		return nil, connectionError(store, err)
	}
	gmailConnection, err := gmail.NewLocalConnection(store, blogpost.GmailConnectionName, options.GmailOptions...)
	if err != nil {
		return nil, connectionError(store, err)
	}
	models, err := loadModels(store)
	if err != nil {
		return nil, err
	}
	key, err := subscribers.LoadSigningKey(options.Config.Newsletter.UnsubscribeKeyFile)
	if err != nil {
		return nil, err
	}
	links := subscribers.NewUnsubscribeLinks(options.Config.Newsletter.PublicBaseURL, key)
	editorLinks := blogpost.NewEditorLinks(options.Config.Newsletter.PublicBaseURL, key)
	directory := &subscriberDirectory{}
	blogPosts := blogpost.NewFlow(blogpost.Dependencies{
		Slack: slackConnection, GitHub: githubConnection, LLM: llmConnection, Gmail: gmailConnection,
		Models: models, Config: options.Config, Subscribers: directory, Unsubscribe: links, Editor: editorLinks,
	})
	registry, err := dex.NewRegistry([]dex.Flow{blogPosts, subscribers.Flow{}})
	if err != nil {
		return nil, fmt.Errorf("register Flows: %w", err)
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: options.BlobCacheDirectory, MaxBytes: 512 << 20, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("create Dex blob cache: %w", err)
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		BindAddress: options.WorkerBindAddress, WorkerTarget: dex.WorkerTarget{Address: options.WorkerTarget},
		FlowServiceAddress: options.FlowServiceAddress, Logger: logger,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create Dex Worker: %w", err), cache.Close())
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: options.FlowServiceAddress, WorkerTarget: worker.WorkerTarget(), Logger: logger,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create Dex Client: %w", err), stopWorker(worker), cache.Close())
	}
	directory.service = subscribers.NewService(client, links)
	application := &Application{
		Subscribers: directory.service, Drafts: blogpost.NewEditorService(client, blogPosts, editorLinks), BlogPosts: blogPosts, Client: client,
		logger: logger, worker: worker, cache: cache, workerBindAddress: options.WorkerBindAddress,
	}
	if !options.WithoutSlackTrigger {
		application.trigger, err = newSlackTrigger(store, client, blogPosts, logger, options.SlackOptions)
		if err != nil {
			return nil, errors.Join(err, application.Close())
		}
	}
	return application, nil
}

func newSlackTrigger(store *localconfig.Store, client *dex.Client, flow *blogpost.Flow, logger *slog.Logger, options []slack.Option) (*slack.MessageTriggerRunner, error) {
	requestTrigger := slack.ChannelThreadCreatedTriggerDefinition.Trigger.TriggerName
	var requestConfiguration slack.ChannelThreadCreatedTriggerConfiguration
	if err := store.DecodeTriggerConfiguration(slack.ConnectorID, blogpost.SlackConnectionName, requestTrigger, blogpost.RequestTriggerBinding, &requestConfiguration); err != nil {
		return nil, fmt.Errorf("%w; pick the blog request channel for Trigger binding %q in Dex Web Connections, then restart", err, blogpost.RequestTriggerBinding)
	}
	requestFilter, err := blogpost.NewRequestTriggerFilter(requestConfiguration)
	if err != nil {
		return nil, err
	}
	reviewTrigger := slack.ThreadReplyCreatedTriggerDefinition.Trigger.TriggerName
	var reviewConfiguration slack.ThreadReplyCreatedTriggerConfiguration
	if err := store.DecodeTriggerConfiguration(slack.ConnectorID, blogpost.SlackConnectionName, reviewTrigger, blogpost.ReviewTriggerBinding, &reviewConfiguration); err != nil {
		return nil, fmt.Errorf("%w; pick the channel and reviewers for Trigger binding %q in Dex Web Connections, then restart", err, blogpost.ReviewTriggerBinding)
	}
	reviewFilter, err := blogpost.NewReviewTriggerFilter(reviewConfiguration)
	if err != nil {
		return nil, fmt.Errorf("Trigger binding %q: %w", blogpost.ReviewTriggerBinding, err)
	}
	bindingLogger := func(trigger, binding string) *slog.Logger {
		return logger.With("connector", slack.ConnectorID, "connection", blogpost.SlackConnectionName, "trigger", trigger, "binding", binding)
	}
	return slack.NewLocalMessageTriggerRunner(store, blogpost.SlackConnectionName, slack.LocalMessageTriggerRunnerConfig{
		ChannelThreadCreatedRoutes: []slack.LocalChannelThreadCreatedTriggerRoute{{
			BindingName: blogpost.RequestTriggerBinding,
			Target: sdkgo.NewDexFlowTriggerTarget(client, flow, requestFilter, blogpost.ResolveRequestFlowID, blogpost.MapToSlackRequest,
				sdkgo.WithTriggerLogger(bindingLogger(requestTrigger, blogpost.RequestTriggerBinding))),
		}},
		ThreadReplyCreatedRoutes: []slack.LocalThreadReplyCreatedTriggerRoute{{
			BindingName: blogpost.ReviewTriggerBinding,
			Target: sdkgo.NewDexRPCTriggerTarget(client, flow.ReceiveSlackReview, reviewFilter, blogpost.ResolveReviewFlowID, blogpost.MapToSlackReviewReply,
				sdkgo.WithTriggerLogger(bindingLogger(reviewTrigger, blogpost.ReviewTriggerBinding))),
		}},
	}, append(append([]slack.Option{}, options...), slack.WithLogger(logger))...)
}

func loadModels(store *localconfig.Store) (blogpost.Models, error) {
	picks := map[string]string{}
	for _, stepType := range blogpost.ModelStepTypes {
		loaded, err := localconfig.LoadOperationConfiguration[blogpost.ModelConfiguration](store, blogpost.ModelConfigurationRef(stepType))
		if errors.Is(err, localconfig.ErrConfigurationNotFound) {
			continue
		}
		if err != nil {
			return blogpost.Models{}, err
		}
		picks[stepType] = loaded.Value.Model
	}
	return blogpost.Models{
		Interpret: picks[blogpost.ModelStepTypes[0]], Choose: picks[blogpost.ModelStepTypes[1]],
		WriteBlog: picks[blogpost.ModelStepTypes[2]], WriteNewsletter: picks[blogpost.ModelStepTypes[3]],
	}, nil
}

func connectionError(store *localconfig.Store, err error) error {
	return fmt.Errorf("%w; set up every connection in Dex Web Connections (store %s), then restart", err, store.Path())
}

// Run waits for Dex, starts the Worker, opens the subscriber list, and runs the Slack Trigger until ctx ends.
func (application *Application) Run(ctx context.Context) error {
	if err := waitForDex(ctx, application.Client, application.logger); err != nil {
		return err
	}
	results := make(chan error, 2)
	go func() { results <- application.worker.Start() }()
	// The Worker listens only after it syncs the Attribute indexes a Flow start needs.
	if err := waitForListener(ctx, application.workerBindAddress, results); err != nil {
		return err
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := application.Subscribers.EnsureStarted(startCtx)
	cancel()
	if err != nil {
		return err
	}
	if application.trigger != nil {
		go func() { results <- application.trigger.Run(ctx) }()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-results:
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func (application *Application) Close() error {
	return errors.Join(application.Client.Close(), stopWorker(application.worker), application.cache.Close())
}

func waitForDex(ctx context.Context, client *dex.Client, logger *slog.Logger) error {
	delay := 250 * time.Millisecond
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := client.HealthCheck(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		logger.Warn("dex server unavailable; retrying", "delay", delay, "error", err.Error())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, 10*time.Second)
	}
}

func waitForListener(ctx context.Context, address string, workerResult <-chan error) error {
	for {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			return connection.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-workerResult:
			return fmt.Errorf("start Dex Worker: %w", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func stopWorker(worker *dex.Worker) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return worker.Stop(ctx)
}
