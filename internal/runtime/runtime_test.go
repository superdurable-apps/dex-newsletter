package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/api"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	github "github.com/superdurable/dex-connectors-library/connectors/github"
	"github.com/superdurable/dex-connectors-library/connectors/google/gemini"
	"github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
)

// The log messages below are the operator-facing contract for a Process that
// Dex Web has not fully configured yet.
const (
	connectionFileUnsetMessage     = "no connector connection file; set it to the path shown by Dex Web Connections"
	connectionFileMissingMessage   = "connector connection file does not exist yet; configure connections in Dex Web Connections and restart the application"
	connectionNotConfiguredMessage = "connector connection is not configured; configure it in Dex Web Connections"
	slackTriggerMissingMessage     = "Slack request Trigger is not configured; configure the channel in Dex Web Connections"
)

func TestLoadConnectorStoreWithoutVariableReturnsNilStore(t *testing.T) {
	t.Setenv(localconfig.EnvironmentVariable, "")
	if err := os.Unsetenv(localconfig.EnvironmentVariable); err != nil {
		t.Fatal(err)
	}
	logger, logs := newLogRecorder()

	store, err := loadConnectorStore(logger)
	if err != nil {
		t.Fatalf("loadConnectorStore() error = %v; want nil", err)
	}
	if store != nil {
		t.Fatal("loadConnectorStore() returned a store; want nil so the unconfigured connections are used")
	}
	warnings := logs.find(t, connectionFileUnsetMessage)
	if len(warnings) != 1 {
		t.Fatalf("got %d unset-variable warnings; want 1 in %v", len(warnings), logs.records(t))
	}
	warnings[0].require(t, "level", "WARN")
	warnings[0].require(t, "environment_variable", localconfig.EnvironmentVariable)
}

func TestLoadConnectorStoreTreatsMissingFileAsUnconfigured(t *testing.T) {
	directory := t.TempDir()
	for name, path := range map[string]string{
		"file not written yet":      filepath.Join(directory, "connections.json"),
		"directory not created yet": filepath.Join(directory, "connectors", "connections.json"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(localconfig.EnvironmentVariable, path)
			logger, logs := newLogRecorder()

			store, err := loadConnectorStore(logger)
			if err != nil {
				t.Fatalf("loadConnectorStore() error = %v; a file Dex Web has not written yet must not stop startup", err)
			}
			if store != nil {
				t.Fatal("loadConnectorStore() returned a store for a missing file; want nil")
			}
			warnings := logs.find(t, connectionFileMissingMessage)
			if len(warnings) != 1 {
				t.Fatalf("got %d missing-file warnings; want 1 in %v", len(warnings), logs.records(t))
			}
			warnings[0].require(t, "level", "WARN")
			warnings[0].require(t, "environment_variable", localconfig.EnvironmentVariable)
			warnings[0].require(t, "path", path)
		})
	}
}

func TestLoadConnectorStoreLogsAbsolutePathForRelativeMissingFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(localconfig.EnvironmentVariable, "connections.json")
	want, err := filepath.Abs("connections.json")
	if err != nil {
		t.Fatal(err)
	}
	logger, logs := newLogRecorder()

	store, err := loadConnectorStore(logger)
	if err != nil || store != nil {
		t.Fatalf("loadConnectorStore() = (store %t, %v); want (false, nil)", store != nil, err)
	}
	warnings := logs.find(t, connectionFileMissingMessage)
	if len(warnings) != 1 {
		t.Fatalf("got %d missing-file warnings; want 1 in %v", len(warnings), logs.records(t))
	}
	warnings[0].require(t, "path", want)
}

func TestLoadConnectorStoreKeepsOtherLoadErrorsFatal(t *testing.T) {
	for name, testCase := range map[string]struct {
		prepare func(t *testing.T, directory string) string
		want    string
	}{
		"malformed file": {
			prepare: func(t *testing.T, directory string) string {
				path := filepath.Join(directory, "connections.json")
				writePrivateFile(t, path, []byte(`{"schemaVersion":`))
				return path
			},
			want: "decode connector configuration file",
		},
		"unsupported schema version": {
			prepare: func(t *testing.T, directory string) string {
				path := filepath.Join(directory, "connections.json")
				writePrivateFile(t, path, []byte(`{"schemaVersion":"connectors.dex.dev/local-connections/v0","connections":[]}`))
				return path
			},
			want: "unsupported connector configuration schema version",
		},
		"broad permissions": {
			prepare: func(t *testing.T, directory string) string {
				path := writeConnectionsFile(t, directory, slackConnectionFixture)
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want: "permissions must be 0600",
		},
		"symlink": {
			prepare: func(t *testing.T, directory string) string {
				target := writeConnectionsFileAt(t, filepath.Join(directory, "target.json"), slackConnectionFixture)
				path := filepath.Join(directory, "connections.json")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want: "must be a regular file",
		},
		"dangling symlink": {
			prepare: func(t *testing.T, directory string) string {
				path := filepath.Join(directory, "connections.json")
				if err := os.Symlink(filepath.Join(directory, "missing.json"), path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want: "must be a regular file",
		},
		"directory": {
			prepare: func(t *testing.T, directory string) string {
				path := filepath.Join(directory, "connections.json")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want: "must be a regular file",
		},
		"malformed use configurations file": {
			prepare: func(t *testing.T, directory string) string {
				path := writeConnectionsFile(t, directory, slackConnectionFixture)
				writePrivateFile(t, filepath.Join(directory, localconfig.UseConfigurationsFileName), []byte(`{"schemaVersion":`))
				return path
			},
			want: "decode connector use configuration file",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(localconfig.EnvironmentVariable, testCase.prepare(t, t.TempDir()))
			logger, logs := newLogRecorder()

			store, err := loadConnectorStore(logger)
			if err == nil {
				t.Fatal("loadConnectorStore() succeeded; want a fatal load error")
			}
			if store != nil {
				t.Error("loadConnectorStore() returned a store with its error")
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Errorf("error %q matches fs.ErrNotExist; only an absent file may be treated as unconfigured", err)
			}
			for _, want := range []string{"load connector connections", testCase.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if warnings := logs.find(t, connectionFileMissingMessage); len(warnings) != 0 {
				t.Errorf("a fatal load error was logged as a missing file: %v", warnings)
			}
		})
	}
}

func TestLoadConnectorStoreLoadsConnectionFile(t *testing.T) {
	path := writeConnectionsFile(t, t.TempDir(), slackConnectionFixture)
	t.Setenv(localconfig.EnvironmentVariable, path)
	logger, logs := newLogRecorder()

	store, err := loadConnectorStore(logger)
	if err != nil {
		t.Fatalf("loadConnectorStore() error = %v", err)
	}
	if store == nil {
		t.Fatal("loadConnectorStore() returned no store for an existing file")
	}
	if store.Path() != path {
		t.Errorf("store path = %q; want %q", store.Path(), path)
	}
	if records := logs.records(t); len(records) != 0 {
		t.Errorf("loading a valid file logged %v; want nothing", records)
	}
}

func TestNewProcessConnectionsFallsBackOnlyForMissingConnections(t *testing.T) {
	store := loadStoreFile(t, writeConnectionsFile(t, t.TempDir(), slackConnectionFixture))
	logger, logs := newLogRecorder()

	if _, err := newProcessConnections(store, logger); err != nil {
		t.Fatalf("newProcessConnections() error = %v", err)
	}
	unconfigured := map[string]string{}
	for _, record := range logs.find(t, connectionNotConfiguredMessage) {
		record.require(t, "level", "WARN")
		connector, _ := record["connector"].(string)
		connection, _ := record["connection"].(string)
		if _, duplicate := unconfigured[connector]; duplicate {
			t.Errorf("connector %q was reported twice", connector)
		}
		unconfigured[connector] = connection
		if text, _ := record["error"].(string); !strings.Contains(text, "is not configured") {
			t.Errorf("warning for %s error = %q; want the store's not-configured error", connector, text)
		}
	}
	want := map[string]string{
		github.ConnectorID: techblog.GitHubConnectionName,
		gemini.ConnectorID: techblog.GeminiConnectionName,
		gmail.ConnectorID:  techblog.GmailConnectionName,
	}
	if !reflect.DeepEqual(unconfigured, want) {
		t.Errorf("unconfigured connections = %v; want %v (only Slack is configured)", unconfigured, want)
	}
	if records := logs.records(t); len(records) != len(want) {
		t.Errorf("got %d log records; want only the %d not-configured warnings: %v", len(records), len(want), records)
	}
}

func TestNewProcessConnectionsWithoutStoreUsesUnconfiguredConnections(t *testing.T) {
	logger, logs := newLogRecorder()

	if _, err := newProcessConnections(nil, logger); err != nil {
		t.Fatalf("newProcessConnections(nil) error = %v", err)
	}
	// loadConnectorStore already warned about the absent file once.
	if records := logs.records(t); len(records) != 0 {
		t.Errorf("newProcessConnections(nil) logged %v; want nothing", records)
	}
}

func TestLocalOrUnconfiguredUsesConfiguredStoreConnection(t *testing.T) {
	store := loadStoreFile(t, writeConnectionsFile(t, t.TempDir(), slackConnectionFixture))
	logger, logs := newLogRecorder()
	fallbackCalls := 0

	_, err := localOrUnconfigured(store, logger, slack.ConnectorID, techblog.SlackConnectionName, slack.NewLocalConnection,
		func(credentials sdkgo.CredentialProvider[slack.Credentials]) (slack.Connection, error) {
			fallbackCalls++
			return newUnconfiguredSlackConnection(credentials)
		})
	if err != nil {
		t.Fatalf("localOrUnconfigured() error = %v", err)
	}
	if fallbackCalls != 0 {
		t.Errorf("the unconfigured fallback ran %d times for a configured connection; want 0", fallbackCalls)
	}
	if records := logs.records(t); len(records) != 0 {
		t.Errorf("a configured connection logged %v; want nothing", records)
	}
}

func TestLocalOrUnconfiguredFallbackCredentialsPointToDexWeb(t *testing.T) {
	slackOnlyStore := loadStoreFile(t, writeConnectionsFile(t, t.TempDir(), slackConnectionFixture))
	for name, testCase := range map[string]struct {
		store        *localconfig.Store
		wantWarnings int
	}{
		"connection absent from the store": {store: slackOnlyStore, wantWarnings: 1},
		"no connector store":               {store: nil, wantWarnings: 0},
	} {
		t.Run(name, func(t *testing.T) {
			logger, logs := newLogRecorder()
			var fallbackCredentials []sdkgo.CredentialProvider[gmail.Credentials]

			_, err := localOrUnconfigured(testCase.store, logger, gmail.ConnectorID, techblog.GmailConnectionName, gmail.NewLocalConnection,
				func(credentials sdkgo.CredentialProvider[gmail.Credentials]) (gmail.Connection, error) {
					fallbackCredentials = append(fallbackCredentials, credentials)
					return newUnconfiguredGmailConnection(credentials)
				})
			if err != nil {
				t.Fatalf("localOrUnconfigured() error = %v", err)
			}
			if len(fallbackCredentials) != 1 {
				t.Fatalf("the unconfigured fallback ran %d times; want 1", len(fallbackCredentials))
			}
			_, err = fallbackCredentials[0].Resolve(sdkgo.Call{Connection: sdkgo.ConnectionRef{Provider: "google", Name: techblog.GmailConnectionName}})
			if err == nil {
				t.Fatal("unconfigured credentials resolved; want an error until Dex Web configures the connection")
			}
			for _, want := range []string{
				fmt.Sprintf("connector %q", gmail.ConnectorID),
				fmt.Sprintf("connection %q", techblog.GmailConnectionName),
				"configure it in Dex Web",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("credential error %q does not contain %q", err, want)
				}
			}
			warnings := logs.find(t, connectionNotConfiguredMessage)
			if len(warnings) != testCase.wantWarnings {
				t.Fatalf("got %d not-configured warnings; want %d", len(warnings), testCase.wantWarnings)
			}
			for _, warning := range warnings {
				warning.require(t, "connector", gmail.ConnectorID)
				warning.require(t, "connection", techblog.GmailConnectionName)
			}
		})
	}
}

func TestLoadProcessInputsStartsOnFreshMachine(t *testing.T) {
	t.Setenv(ProcessConfigurationFileEnvironmentVariable, "")
	t.Setenv(localconfig.EnvironmentVariable, filepath.Join(t.TempDir(), "connections.json"))
	logger, logs := newLogRecorder()

	inputs, err := loadProcessInputs(logger)
	if err != nil {
		t.Fatalf("loadProcessInputs() error = %v; a fresh machine must start with unconfigured connections", err)
	}
	if inputs.store != nil {
		t.Error("loadProcessInputs() loaded a store from a missing file")
	}
	if !reflect.DeepEqual(inputs.configuration, config.Default()) {
		t.Error("loadProcessInputs() without a process configuration file did not use config.Default()")
	}
	records := logs.records(t)
	if len(records) != 1 {
		t.Fatalf("got log records %v; want only the missing-file warning", records)
	}
	records[0].require(t, "msg", connectionFileMissingMessage)
}

func TestNewSlackRequestTriggerRunnerWaitsForDexWebConfiguration(t *testing.T) {
	slackStore := loadStoreFile(t, writeConnectionsFile(t, t.TempDir(), slackConnectionFixture))
	for name, testCase := range map[string]struct {
		store        *localconfig.Store
		wantWarnings int
	}{
		"no connector store": {store: nil, wantWarnings: 0},
		"no Trigger binding": {store: slackStore, wantWarnings: 1},
	} {
		t.Run(name, func(t *testing.T) {
			logger, logs := newLogRecorder()

			runner, err := newSlackRequestTriggerRunner(testCase.store, nil, nil, logger)
			if err != nil {
				t.Fatalf("newSlackRequestTriggerRunner() error = %v", err)
			}
			if runner != nil {
				t.Error("newSlackRequestTriggerRunner() returned a runner without a Trigger binding")
			}
			if warnings := logs.find(t, slackTriggerMissingMessage); len(warnings) != testCase.wantWarnings {
				t.Errorf("got %d missing-Trigger warnings; want %d", len(warnings), testCase.wantWarnings)
			}
		})
	}
}

// connectionFixture is one record of the Dex Web local connection file
// (localconfig.SchemaVersion). The credentials are fake.
type connectionFixture struct {
	connectorID    string
	modulePath     string
	moduleVersion  string
	provider       string
	connectionName string
	credentials    map[string]string
}

var (
	slackConnectionFixture = connectionFixture{
		connectorID:    slack.ConnectorID,
		modulePath:     "github.com/superdurable/dex-connectors-library/connectors/slack",
		moduleVersion:  "v0.10.0",
		provider:       "slack",
		connectionName: techblog.SlackConnectionName,
		credentials:    map[string]string{"bot_token": "xoxb-test", "user_token": "xoxp-test", "app_token": "xapp-test"},
	}
)

// writeConnectionsFile writes directory/connections.json as Dex Web does.
func writeConnectionsFile(t *testing.T, directory string, fixtures ...connectionFixture) string {
	t.Helper()
	return writeConnectionsFileAt(t, filepath.Join(directory, "connections.json"), fixtures...)
}

func writeConnectionsFileAt(t *testing.T, path string, fixtures ...connectionFixture) string {
	t.Helper()
	connections := make([]map[string]any, 0, len(fixtures))
	for _, fixture := range fixtures {
		connections = append(connections, map[string]any{
			"connectorId":    fixture.connectorID,
			"modulePath":     fixture.modulePath,
			"moduleVersion":  fixture.moduleVersion,
			"provider":       fixture.provider,
			"connectionName": fixture.connectionName,
			"configuration":  map[string]any{},
			"credentials":    fixture.credentials,
		})
	}
	writePrivateJSON(t, path, map[string]any{"schemaVersion": localconfig.SchemaVersion, "connections": connections})
	return path
}

func writePrivateJSON(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, path, contents)
}

// writePrivateFile writes a 0600 regular file, the only mode the connector
// store loader accepts.
func writePrivateFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadStoreFile(t *testing.T, path string) *localconfig.Store {
	t.Helper()
	store, err := localconfig.LoadFile(path)
	if err != nil {
		t.Fatalf("load connector store fixture: %v", err)
	}
	return store
}

// logRecorder captures JSON log records so tests can assert on the warnings an
// operator sees.
type logRecorder struct{ output bytes.Buffer }

type logRecord map[string]any

func newLogRecorder() (*slog.Logger, *logRecorder) {
	recorder := &logRecorder{}
	return slog.New(slog.NewJSONHandler(&recorder.output, &slog.HandlerOptions{Level: slog.LevelDebug})), recorder
}

func (recorder *logRecorder) records(t *testing.T) []logRecord {
	t.Helper()
	records := []logRecord{}
	decoder := json.NewDecoder(bytes.NewReader(recorder.output.Bytes()))
	for decoder.More() {
		var record logRecord
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func (recorder *logRecorder) find(t *testing.T, message string) []logRecord {
	t.Helper()
	var found []logRecord
	for _, record := range recorder.records(t) {
		if record["msg"] == message {
			found = append(found, record)
		}
	}
	return found
}

func (record logRecord) require(t *testing.T, key string, want string) {
	t.Helper()
	if got, _ := record[key].(string); got != want {
		t.Errorf("log record %q = %q; want %q in %v", key, got, want, record)
	}
}

type scriptedSubscriberAdder struct {
	result   techblog.AddNewsletterSubscriberResult
	err      error
	deadline bool
}

func (adder *scriptedSubscriberAdder) AddNewsletterSubscriber(ctx context.Context, _ string) (techblog.AddNewsletterSubscriberResult, error) {
	_, adder.deadline = ctx.Deadline()
	return adder.result, adder.err
}

func TestNewsletterSubscriptionsMapsListOutcomesToAPIErrors(t *testing.T) {
	for name, testCase := range map[string]struct {
		result    techblog.AddNewsletterSubscriberResult
		err       error
		wantEmail string
		wantErr   error
	}{
		"added":              {result: techblog.AddNewsletterSubscriberResult{Outcome: techblog.SubscriptionAdded, Email: "a@example.com"}, wantEmail: "a@example.com"},
		"already subscribed": {result: techblog.AddNewsletterSubscriberResult{Outcome: techblog.SubscriptionAlreadySubscribed, Email: "a@example.com"}, wantEmail: "a@example.com"},
		"invalid address":    {result: techblog.AddNewsletterSubscriberResult{Outcome: techblog.SubscriptionInvalidAddress}, wantErr: api.ErrInvalidEmailAddress},
		"list full":          {result: techblog.AddNewsletterSubscriberResult{Outcome: techblog.SubscriptionListFull}, wantErr: api.ErrSubscriberListFull},
	} {
		t.Run(name, func(t *testing.T) {
			adder := &scriptedSubscriberAdder{result: testCase.result, err: testCase.err}
			email, err := newsletterSubscriptions{list: adder}.Subscribe(context.Background(), "A@Example.com")
			if email != testCase.wantEmail || !errors.Is(err, testCase.wantErr) || (testCase.wantErr == nil && err != nil) {
				t.Fatalf("Subscribe() = %q, %v; want %q, %v", email, err, testCase.wantEmail, testCase.wantErr)
			}
			if !adder.deadline {
				t.Error("Subscribe() called the list without a deadline")
			}
		})
	}
}

func TestNewsletterSubscriptionsReportsUnavailableListWithoutTheAddress(t *testing.T) {
	adder := &scriptedSubscriberAdder{err: errors.New("dial tcp 127.0.0.1:8801: connection refused")}
	_, err := newsletterSubscriptions{list: adder}.Subscribe(context.Background(), "secret.reader@example.com")
	if err == nil || errors.Is(err, api.ErrInvalidEmailAddress) || errors.Is(err, api.ErrSubscriberListFull) {
		t.Fatalf("Subscribe() error = %v; want an unavailable error", err)
	}
	if strings.Contains(err.Error(), "secret.reader") {
		t.Errorf("error %q contains the subscriber address", err)
	}
	adder = &scriptedSubscriberAdder{result: techblog.AddNewsletterSubscriberResult{Outcome: "surprise"}}
	if _, err := (newsletterSubscriptions{list: adder}).Subscribe(context.Background(), "a@example.com"); err == nil {
		t.Fatal("Subscribe() accepted an unknown outcome")
	}
}

func TestStartSubscriberListRetriesUntilTheListStarts(t *testing.T) {
	logger, logs := newLogRecorder()
	attempts := 0
	err := startSubscriberList(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("Dex is starting")
		}
		return nil
	}, logger)
	if err != nil || attempts != 3 {
		t.Fatalf("startSubscriberList() = %v after %d attempts; want nil after 3", err, attempts)
	}
	if warnings := logs.find(t, "could not start the newsletter subscriber list Flow; retrying"); len(warnings) != 2 {
		t.Errorf("got %d retry logs; want 2", len(warnings))
	}
}

func TestStartSubscriberListStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger, _ := newLogRecorder()
	err := startSubscriberList(ctx, func(context.Context) error {
		cancel()
		return errors.New("Dex is unavailable")
	}, logger)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startSubscriberList() = %v; want context.Canceled", err)
	}
}
