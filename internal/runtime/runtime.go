// Package runtime composes the tech blog newsletter Process: configuration,
// connector connections, Flows, the Dex Worker and Client, the Slack request
// Trigger, and the newsletter subscriber list behind the subscription API.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/api"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/unsubscribe"
	github "github.com/superdurable/dex-connectors-library/connectors/github"
	"github.com/superdurable/dex-connectors-library/connectors/google/gemini"
	"github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ProcessConfigurationFileEnvironmentVariable names the optional process
// configuration file. Without it the built-in defaults are used.
const ProcessConfigurationFileEnvironmentVariable = "TECH_BLOG_CONFIG_FILE"

// UnsubscribeKeyFileEnvironmentVariable names the required file holding the
// base64 key that signs unsubscribe links, such as the output of
// `openssl rand -base64 32`. Changing the key invalidates every link sent.
const UnsubscribeKeyFileEnvironmentVariable = "TECH_BLOG_UNSUBSCRIBE_KEY_FILE"

// subscriptionTimeout bounds one subscription request's call into Dex.
const subscriptionTimeout = 10 * time.Second

// Runtime owns the Dex Worker, Client, blob cache, Slack Trigger runner, and
// the client of the newsletter subscriber list Flow.
type Runtime struct {
	configuration  config.ProcessConfiguration
	logger         *slog.Logger
	worker         *dex.Worker
	client         *dex.Client
	cache          *blobcache.Cache
	subscriberList techblog.NewsletterSubscriberListClient
	triggerRunner  *slack.MessageTriggerRunner
	stopBackground context.CancelFunc
	backgroundDone chan struct{}
	closeOnce      sync.Once
}

// New loads configuration and connections and constructs every component. A
// missing connection or Trigger binding is logged, not fatal, so Dex Web can
// show what still needs configuring.
func New(logger *slog.Logger) (*Runtime, error) {
	inputs, err := loadProcessInputs(logger)
	if err != nil {
		return nil, err
	}
	configuration, store, connections := inputs.configuration, inputs.store, inputs.connections
	artifactStore, err := techblog.NewDirectoryBlogArtifactStore(configuration.Blog.ArtifactDirectory)
	if err != nil {
		return nil, err
	}
	unsubscribeLinks, err := unsubscribe.NewLinks(inputs.unsubscribeKey, configuration.Newsletter.SubscriptionPageURL)
	if err != nil {
		return nil, err
	}
	if pointsAtThisMachine(configuration.Newsletter.SubscriptionPageURL) {
		logger.Warn("newsletter.subscriptionPageUrl points at this machine, so unsubscribe links in sent newsletters work only here; set it to the page address readers use",
			"subscription_page_url", configuration.Newsletter.SubscriptionPageURL)
	}
	languageModel := techblog.NewLanguageModelGenerationFlow(connections.gemini)
	research := techblog.NewRepositoryChangeResearchFlow(configuration, connections.github, languageModel)
	subscriberListFlow := techblog.NewNewsletterSubscriberListFlow(configuration, inputs.unsubscribeKey)
	// The Dex Client needs the registry, and the registry needs the Flows, so
	// the subscriber list client resolves the Client when it is first called.
	var dexClient *dex.Client
	subscriberList := techblog.NewNewsletterSubscriberListClient(subscriberListFlow, techblog.NewsletterSubscriberListFlowID,
		func() *dex.Client { return dexClient })
	newsletter := techblog.NewTechBlogNewsletterFlow(techblog.TechBlogNewsletterFlowDependencies{
		Configuration:           configuration,
		SlackConnection:         connections.slack,
		GmailConnection:         connections.gmail,
		SubscriberList:          subscriberList,
		UnsubscribeLinks:        unsubscribeLinks,
		BlogArtifactStore:       artifactStore,
		LanguageModelGeneration: languageModel,
		RepositoryResearch:      research,
	})
	registry, err := dex.NewRegistry([]dex.Flow{newsletter, research, languageModel, subscriberListFlow})
	if err != nil {
		return nil, fmt.Errorf("register tech blog Flows: %w", err)
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir: environment("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-tech-blog-blobs")), MaxBytes: 1 << 30, Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create Dex blob cache: %w", err)
	}
	flowServiceAddress := environment("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801")
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		BindAddress:        environment("DEX_WORKER_BIND_ADDRESS", "127.0.0.1:8811"),
		WorkerTarget:       dex.WorkerTarget{Address: environment("DEX_WORKER_TARGET", "127.0.0.1:8811")},
		FlowServiceAddress: flowServiceAddress, Logger: logger,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create Dex Worker: %w", err), cache.Close())
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress, WorkerTarget: worker.WorkerTarget(), Logger: logger,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create Dex Client: %w", err), stopWorker(worker), cache.Close())
	}
	dexClient = client
	runtime := &Runtime{configuration: configuration, logger: logger, worker: worker, client: client, cache: cache, subscriberList: subscriberList}
	runtime.triggerRunner, err = newSlackRequestTriggerRunner(store, client, newsletter, logger)
	if err != nil {
		return nil, errors.Join(err, runtime.Close())
	}
	return runtime, nil
}

// ApplicationName is shown by the application shell.
func (runtime *Runtime) ApplicationName() string { return runtime.configuration.ApplicationName }

// DexWebURL links the application shell to Dex Web.
func (runtime *Runtime) DexWebURL() string { return runtime.configuration.DexWebURL }

// Client exposes the Dex Client for integration tests and tools.
func (runtime *Runtime) Client() *dex.Client { return runtime.client }

// NewsletterSubscriptions adds subscription-form addresses to the newsletter
// subscriber list Flow.
func (runtime *Runtime) NewsletterSubscriptions() api.NewsletterSubscriptions {
	return newsletterSubscriptions{list: runtime.subscriberList}
}

// StartWorker starts the Worker and, once Dex answers a health check, starts
// the newsletter subscriber list Flow and the Slack Trigger runner
// independently, so a list that cannot start never blocks Slack requests. The
// channel receives the first fatal error.
func (runtime *Runtime) StartWorker() <-chan error {
	result := make(chan error, 2)
	go func() { result <- runtime.worker.Start() }()
	backgroundContext, cancel := context.WithCancel(context.Background())
	runtime.stopBackground = cancel
	runtime.backgroundDone = make(chan struct{})
	go func() {
		defer close(runtime.backgroundDone)
		if err := waitForDexServer(backgroundContext, runtime.client.HealthCheck, runtime.logger); err != nil {
			return
		}
		var background sync.WaitGroup
		background.Go(func() {
			_ = startSubscriberList(backgroundContext, runtime.subscriberList.StartList, runtime.logger)
		})
		if runtime.triggerRunner != nil {
			background.Go(func() {
				if err := runtime.triggerRunner.Run(backgroundContext); err != nil && !errors.Is(err, context.Canceled) {
					result <- fmt.Errorf("run Slack request Trigger: %w", err)
				}
			})
		}
		background.Wait()
	}()
	return result
}

// Close stops the Trigger runner, Worker, Client, and cache.
func (runtime *Runtime) Close() error {
	var err error
	runtime.closeOnce.Do(func() {
		if runtime.stopBackground != nil {
			runtime.stopBackground()
			<-runtime.backgroundDone
		}
		err = errors.Join(stopWorker(runtime.worker), runtime.client.Close(), runtime.cache.Close())
	})
	return err
}

// processInputs is everything New loads before it composes the Flows: the
// process configuration, the optional connector store, and one connection per
// connector.
type processInputs struct {
	configuration  config.ProcessConfiguration
	unsubscribeKey unsubscribe.Key
	store          *localconfig.Store
	connections    processConnections
}

// loadProcessInputs reads the process configuration and connector store from
// the environment and builds every connection. It needs no Dex server.
func loadProcessInputs(logger *slog.Logger) (processInputs, error) {
	configuration, err := config.Load(os.Getenv(ProcessConfigurationFileEnvironmentVariable))
	if err != nil {
		return processInputs{}, err
	}
	unsubscribeKey, err := loadUnsubscribeKey()
	if err != nil {
		return processInputs{}, err
	}
	store, err := loadConnectorStore(logger)
	if err != nil {
		return processInputs{}, err
	}
	connections, err := newProcessConnections(store, logger)
	if err != nil {
		return processInputs{}, err
	}
	return processInputs{configuration: configuration, unsubscribeKey: unsubscribeKey, store: store, connections: connections}, nil
}

// pointsAtThisMachine reports whether pageURL names a loopback or localhost
// host, which readers on other machines cannot open.
func pointsAtThisMachine(pageURL string) bool {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// loadUnsubscribeKey reads the key named by TECH_BLOG_UNSUBSCRIBE_KEY_FILE. The
// key is required: every newsletter carries an unsubscribe link.
func loadUnsubscribeKey() (unsubscribe.Key, error) {
	path := os.Getenv(UnsubscribeKeyFileEnvironmentVariable)
	if path == "" {
		return unsubscribe.Key{}, fmt.Errorf("%s is not set; point it at a file holding a base64 key of at least 32 bytes, for example the output of `openssl rand -base64 32` (make dev creates .dex-dev/unsubscribe.key)",
			UnsubscribeKeyFileEnvironmentVariable)
	}
	key, err := unsubscribe.LoadKey(path)
	if err != nil {
		return unsubscribe.Key{}, fmt.Errorf("%s: %w", UnsubscribeKeyFileEnvironmentVariable, err)
	}
	return key, nil
}

type processConnections struct {
	slack  slack.Connection
	github github.Connection
	gemini gemini.Connection
	gmail  gmail.Connection
}

func newProcessConnections(store *localconfig.Store, logger *slog.Logger) (processConnections, error) {
	var connections processConnections
	var err error
	if connections.slack, err = localOrUnconfigured(store, logger, slack.ConnectorID, techblog.SlackConnectionName,
		slack.NewLocalConnection, newUnconfiguredSlackConnection); err != nil {
		return processConnections{}, err
	}
	if connections.github, err = localOrUnconfigured(store, logger, github.ConnectorID, techblog.GitHubConnectionName,
		github.NewLocalConnection, newUnconfiguredGitHubConnection); err != nil {
		return processConnections{}, err
	}
	if connections.gemini, err = localOrUnconfigured(store, logger, gemini.ConnectorID, techblog.GeminiConnectionName,
		gemini.NewLocalConnection, newUnconfiguredGeminiConnection); err != nil {
		return processConnections{}, err
	}
	if connections.gmail, err = localOrUnconfigured(store, logger, gmail.ConnectorID, techblog.GmailConnectionName,
		gmail.NewLocalConnection, newUnconfiguredGmailConnection); err != nil {
		return processConnections{}, err
	}
	return connections, nil
}

func newUnconfiguredSlackConnection(credentials sdkgo.CredentialProvider[slack.Credentials]) (slack.Connection, error) {
	client, err := slack.New(slack.DefaultConfig(), credentials)
	if err != nil {
		return slack.Connection{}, err
	}
	return slack.NewConnection(client, sdkgo.ConnectionRef{Provider: "slack", Name: techblog.SlackConnectionName})
}

func newUnconfiguredGitHubConnection(credentials sdkgo.CredentialProvider[github.Credentials]) (github.Connection, error) {
	client, err := github.New(github.DefaultConfig(), credentials)
	if err != nil {
		return github.Connection{}, err
	}
	return github.NewConnection(client, sdkgo.ConnectionRef{Provider: "github", Name: techblog.GitHubConnectionName})
}

func newUnconfiguredGeminiConnection(credentials sdkgo.CredentialProvider[gemini.Credentials]) (gemini.Connection, error) {
	client, err := gemini.New(gemini.DefaultConfig(), credentials)
	if err != nil {
		return gemini.Connection{}, err
	}
	return gemini.NewConnection(client, sdkgo.ConnectionRef{Provider: "google", Name: techblog.GeminiConnectionName})
}

func newUnconfiguredGmailConnection(credentials sdkgo.CredentialProvider[gmail.Credentials]) (gmail.Connection, error) {
	client, err := gmail.New(gmail.DefaultConfig(), credentials)
	if err != nil {
		return gmail.Connection{}, err
	}
	return gmail.NewConnection(client, sdkgo.ConnectionRef{Provider: "google", Name: techblog.GmailConnectionName})
}

// localOrUnconfigured returns the Dex Web connection when it exists, otherwise
// a connection built on unconfiguredCredentials, whose every credential lookup
// fails until Dex Web configures the connection and the application restarts.
func localOrUnconfigured[C any, O any, Credentials any](
	store *localconfig.Store,
	logger *slog.Logger,
	connectorID string,
	connectionName string,
	newLocal func(*localconfig.Store, string, ...O) (C, error),
	newUnconfigured func(sdkgo.CredentialProvider[Credentials]) (C, error),
) (C, error) {
	if store != nil {
		connection, err := newLocal(store, connectionName)
		if err == nil {
			return connection, nil
		}
		logger.Warn("connector connection is not configured; configure it in Dex Web Connections",
			"connector", connectorID, "connection", connectionName, "error", err.Error())
	}
	connection, err := newUnconfigured(unconfiguredCredentials[Credentials]{connectorID: connectorID, connectionName: connectionName})
	if err != nil {
		var zero C
		return zero, fmt.Errorf("create unconfigured %s connection %s: %w", connectorID, connectionName, err)
	}
	return connection, nil
}

type unconfiguredCredentials[C any] struct {
	connectorID    string
	connectionName string
}

func (credentials unconfiguredCredentials[C]) Resolve(sdkgo.Call) (C, error) {
	var zero C
	return zero, fmt.Errorf("connector %q connection %q is not configured; configure it in Dex Web and restart the application",
		credentials.connectorID, credentials.connectionName)
}

// loadConnectorStore loads the connection file Dex Web writes. An unset
// variable, or a path Dex Web has not written yet (a fresh machine, where
// dexcli creates the directory but not the file), returns a nil store so every
// connection falls back to an unconfigured one. Any other load failure, such
// as a malformed file, broad permissions, or a symlink, is fatal.
func loadConnectorStore(logger *slog.Logger) (*localconfig.Store, error) {
	path := os.Getenv(localconfig.EnvironmentVariable)
	if path == "" {
		logger.Warn("no connector connection file; set it to the path shown by Dex Web Connections",
			"environment_variable", localconfig.EnvironmentVariable)
		return nil, nil
	}
	store, err := localconfig.LoadFromEnvironment()
	if errors.Is(err, fs.ErrNotExist) {
		if absolutePath, absoluteErr := filepath.Abs(path); absoluteErr == nil {
			path = absolutePath
		}
		logger.Warn("connector connection file does not exist yet; configure connections in Dex Web Connections and restart the application",
			"environment_variable", localconfig.EnvironmentVariable, "path", path)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load connector connections: %w", err)
	}
	return store, nil
}

func newSlackRequestTriggerRunner(
	store *localconfig.Store,
	client *dex.Client,
	newsletter *techblog.TechBlogNewsletterFlow,
	logger *slog.Logger,
) (*slack.MessageTriggerRunner, error) {
	if store == nil {
		return nil, nil
	}
	triggerName := slack.ChannelThreadCreatedTriggerDefinition.Trigger.TriggerName
	var binding slack.ChannelThreadCreatedTriggerConfiguration
	if err := store.DecodeTriggerConfiguration(slack.ConnectorID, techblog.SlackConnectionName, triggerName,
		techblog.NewsletterRequestTriggerBinding, &binding); err != nil {
		logger.Warn("Slack request Trigger is not configured; configure the channel in Dex Web Connections", "error", err.Error())
		return nil, nil
	}
	filter, err := techblog.NewsletterRequestTriggerFilter(binding)
	if err != nil {
		return nil, fmt.Errorf("Slack request Trigger configuration: %w", err)
	}
	bindingLogger := logger.With("connector", slack.ConnectorID, "connection", techblog.SlackConnectionName,
		"trigger", triggerName, "binding", techblog.NewsletterRequestTriggerBinding)
	runner, err := slack.NewLocalMessageTriggerRunner(store, techblog.SlackConnectionName, slack.LocalMessageTriggerRunnerConfig{
		ChannelThreadCreatedRoutes: []slack.LocalChannelThreadCreatedTriggerRoute{{
			BindingName: techblog.NewsletterRequestTriggerBinding,
			Target: sdkgo.NewDexFlowTriggerTarget(
				client, newsletter, filter, techblog.ResolveNewsletterRequestFlowID, techblog.MapSlackMessageToNewsletterRequest,
				sdkgo.WithTriggerLogger(bindingLogger),
			),
		}},
	}, slack.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("create Slack request Trigger runner: %w", err)
	}
	return runner, nil
}

// subscriberAdder adds and removes addresses in the newsletter subscriber
// list Flow.
type subscriberAdder interface {
	AddNewsletterSubscriber(ctx context.Context, email string) (techblog.AddNewsletterSubscriberResult, error)
	RemoveNewsletterSubscriber(ctx context.Context, token string) (techblog.RemoveNewsletterSubscriberResult, error)
}

// newsletterSubscriptions adapts the subscriber list Flow to the HTTP API.
type newsletterSubscriptions struct {
	list subscriberAdder
}

// Subscribe maps the list Flow's typed outcome to the API's errors. Errors
// never include the address.
func (subscriptions newsletterSubscriptions) Subscribe(ctx context.Context, email string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
	result, err := subscriptions.list.AddNewsletterSubscriber(ctx, email)
	if err != nil {
		return "", fmt.Errorf("add a newsletter subscriber: %w", err)
	}
	switch result.Outcome {
	case techblog.SubscriptionAdded, techblog.SubscriptionAlreadySubscribed:
		return result.Email, nil
	case techblog.SubscriptionInvalidAddress:
		return "", api.ErrInvalidEmailAddress
	case techblog.SubscriptionListFull:
		return "", api.ErrSubscriberListFull
	default:
		return "", fmt.Errorf("unknown subscription outcome %q", result.Outcome)
	}
}

// Unsubscribe removes the subscriber a link token names. Removed,
// not-subscribed, and invalid tokens all succeed, so the answer never reveals
// who is on the list. Errors never include the token.
func (subscriptions newsletterSubscriptions) Unsubscribe(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
	result, err := subscriptions.list.RemoveNewsletterSubscriber(ctx, token)
	if err != nil {
		return fmt.Errorf("remove a newsletter subscriber: %w", err)
	}
	switch result.Outcome {
	case techblog.UnsubscriptionRemoved, techblog.UnsubscriptionNotSubscribed, techblog.UnsubscriptionInvalidToken:
		return nil
	default:
		return fmt.Errorf("unknown unsubscription outcome %q", result.Outcome)
	}
}

// startSubscriberList starts the subscriber list Flow, retrying with backoff
// until it succeeds or ctx ends. Until it runs, subscriptions answer 503 and
// deliveries retry their read, then hold for attention.
func startSubscriberList(ctx context.Context, start func(context.Context) error, logger *slog.Logger) error {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		attemptContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := start(attemptContext)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Error("could not start the newsletter subscriber list Flow; retrying",
			"flow_id", techblog.NewsletterSubscriberListFlowID, "attempt", attempt, "delay", delay, "error", err.Error())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(2*delay, 30*time.Second)
	}
}

// waitForDexServer returns once Dex answers a health check, backing off from
// 250 milliseconds to 30 seconds.
func waitForDexServer(ctx context.Context, healthCheck func(context.Context) (dex.HealthInfo, error), logger *slog.Logger) error {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		attemptContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := healthCheck(attemptContext)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Warn("Dex server unavailable; retrying", "attempt", attempt, "delay", delay, "error", err.Error())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func stopWorker(worker *dex.Worker) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return worker.Stop(ctx)
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
