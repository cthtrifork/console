package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudhut/connect-client"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/redpanda-data/console/backend/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

var testSeedBroker []string
var testAdminAddress string

const TEST_TOPIC_NAME = "test_redpanda_connect_topic"
const CONNECT_TEST_NETWORK = "redpandaconnecttestnetwork"

func Test_CreateConnector(t *testing.T) {
	fmt.Printf("TEST SEED BROKERS: %+v\n", testSeedBroker)

	kc := startConnect(t, CONNECT_TEST_NETWORK, []string{"redpanda:9092"})
	fmt.Printf("\n%+v\n", kc)

	defer func() {
		ctx := context.Background()

		if err := kc.Terminate(ctx); err != nil {
			panic(err)
		}
	}()

	log, err := zap.NewProduction()
	require.NoError(t, err)

	// create
	connectSvs, err := NewService(config.Connect{
		Enabled: true,
		Clusters: []config.ConnectCluster{
			{
				Name: "redpanda_connect",
				URL:  "http://" + kc.connectHost + ":" + string(kc.connectPort),
			},
		},
	}, log)

	require.NoError(t, err)

	// test
	ctx := context.Background()
	res, connectErr := connectSvs.CreateConnector(ctx, "redpanda_connect", connect.CreateConnectorRequest{
		Name: "http_connect_input",
		Config: map[string]interface{}{
			"connector.class":                           "com.github.castorm.kafka.connect.http.HttpSourceConnector",
			"header.converter":                          "org.apache.kafka.connect.storage.SimpleHeaderConverter",
			"http.request.url":                          "https://httpbin.org/uuid",
			"http.timer.catchup.interval.millis":        "30000",
			"http.timer.interval.millis":                "600000",
			"kafka.topic":                               "httpbin-input",
			"key.converter":                             "org.apache.kafka.connect.json.JsonConverter",
			"key.converter.schemas.enable":              "false",
			"name":                                      "http-source-connector-nbtu",
			"topic.creation.default.partitions":         "1",
			"topic.creation.default.replication.factor": "1",
			"topic.creation.enable":                     "true",
			"value.converter":                           "org.apache.kafka.connect.json.JsonConverter",
			"value.converter.schemas.enable":            "false",
		},
	})

	assert.NoError(t, connectErr.Err)

	rj, _ := json.Marshal(res)
	fmt.Println("RES:")
	fmt.Println(string(rj))
	fmt.Println()

	assert.Fail(t, "FASF")
}

const CONNECT_CONFIGURATION = `key.converter=org.apache.kafka.connect.converters.ByteArrayConverter
value.converter=org.apache.kafka.connect.converters.ByteArrayConverter
group.id=connectors-cluster
offset.storage.topic=_internal_connectors_offsets
config.storage.topic=_internal_connectors_configs
status.storage.topic=_internal_connectors_status
config.storage.replication.factor=-1
offset.storage.replication.factor=-1
status.storage.replication.factor=-1
offset.flush.interval.ms=1000
producer.linger.ms=1
producer.batch.size=131072
client.dns.lookup=resolve_canonical_bootstrap_servers_only`

func startConnect(t *testing.T, network string, bootstrapServers []string) *Connect {
	t.Helper()

	const waitTimeout = 9 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        "docker.cloudsmith.io/redpanda/cloudv2-dev/connectors:1.0.0-dev-1d15b96",
		ExposedPorts: []string{"8083"},
		Env: map[string]string{
			"CONNECT_CONFIGURATION":     CONNECT_CONFIGURATION,
			"CONNECT_BOOTSTRAP_SERVERS": strings.Join(bootstrapServers, ","),
			"CONNECT_GC_LOG_ENABLED":    "false",
			"CONNECT_HEAP_OPTS":         "-Xms512M -Xmx512M",
			"CONNECT_LOG_LEVEL":         "info",
		},
		Networks: []string{
			network,
		},
		NetworkAliases: map[string][]string{
			network: {"connect"},
		},
		Hostname: "redpanda-connect",
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.NetworkMode = "bridge"
		},
		WaitingFor: wait.ForAll(
			wait.ForHTTP("/").WithPort("8083/tcp").
				WithPollInterval(500 * time.Millisecond).
				WithStartupTimeout(waitTimeout),
			// WithResponseMatcher(func(body io.Reader) bool {
			// 	fmt.Println("response body")
			// 	data, _ := io.ReadAll(body)
			// 	fmt.Println(string(data))
			// 	return true
			// }),
			// wait.ForLog("Kafka Connect started").
			// 	WithPollInterval(500 * time.Millisecond).
			// 	WithStartupTimeout(waitTimeout),
		),
	}

	connectContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	connectPort, err := connectContainer.MappedPort(ctx, nat.Port("8083"))
	require.NoError(t, err)

	connectHost, err := connectContainer.Host(ctx)
	require.NoError(t, err)

	kc := Connect{
		Container:   connectContainer,
		connectPort: connectPort,
		connectHost: connectHost,
	}

	return &kc
}

type Connect struct {
	testcontainers.Container

	connectPort nat.Port
	connectHost string
}

func WithNetwork(network, networkAlias string) testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) {
		if len(req.Networks) == 0 {
			req.Networks = []string{}
		}
		req.Networks = append(req.Networks, network)

		if networkAlias != "" {
			if len(req.NetworkAliases) == 0 {
				req.NetworkAliases = map[string][]string{}
			}

			req.NetworkAliases[network] = []string{networkAlias}
		}
	}
}

func WithHostname(hostname string) testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) {
		req.Hostname = hostname
	}
}

func TestMain(m *testing.M) {
	os.Exit(func() int {
		ctx := context.Background()

		testNetwork, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
			// ProviderType: testcontainers.ProviderDocker,
			NetworkRequest: testcontainers.NetworkRequest{
				Name:           CONNECT_TEST_NETWORK,
				CheckDuplicate: true,
			},
		})
		if err != nil {
			panic(err)
		}

		container, err := redpanda.RunContainer(ctx,
			WithNetwork(CONNECT_TEST_NETWORK, "redpanda"),
			WithHostname("redpanda"),
			testcontainers.WithHostConfigModifier(func(hostConfig *container.HostConfig) {
				hostConfig.NetworkMode = "bridge"
			}),
		)
		if err != nil {
			panic(err)
		}

		defer func() {
			if err := container.Terminate(ctx); err != nil {
				panic(err)
			}

			if err := testNetwork.Remove(ctx); err != nil {
				panic(err)
			}
		}()

		seedBroker, err := container.KafkaSeedBroker(ctx)
		if err != nil {
			panic(err)
		}

		testSeedBroker = []string{seedBroker}

		testAdminAddress, err = container.AdminAPIAddress(ctx)
		if err != nil {
			panic(err)
		}

		// create a long lived stock test topic
		kafkaCl, err := kgo.NewClient(
			kgo.SeedBrokers(seedBroker),
		)
		if err != nil {
			panic(err)
		}

		kafkaAdmCl := kadm.NewClient(kafkaCl)
		_, err = kafkaAdmCl.CreateTopic(ctx, 1, 1, nil, TEST_TOPIC_NAME)
		if err != nil {
			panic(err)
		}

		kafkaCl.Close()

		return m.Run()
	}())
}
