package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cloudhut/connect-client"
	"github.com/redpanda-data/console/backend/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	tc "github.com/testcontainers/testcontainers-go/modules/compose"
)

var testSeedBrokers []string

const TEST_TOPIC_NAME = "test_redpanda_connect_topic"

func Test_CreateConnector(t *testing.T) {
	fmt.Printf("TEST SEED BROKERS: %+v\n", testSeedBrokers)

	log, err := zap.NewProduction()
	require.NoError(t, err)

	// create
	connectSvs, err := NewService(config.Connect{
		Enabled: true,
		Clusters: []config.ConnectCluster{
			{
				Name: "redpanda_connect",
				URL:  "http://0.0.0.0:8083",
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
			"name":                                      "http_connect_input",
			"topic.creation.default.partitions":         "1",
			"topic.creation.default.replication.factor": "1",
			"topic.creation.enable":                     "true",
			"value.converter":                           "org.apache.kafka.connect.json.JsonConverter",
			"value.converter.schemas.enable":            "false",
		},
	})

	assert.Nil(t, connectErr)

	rj, _ := json.Marshal(res)
	fmt.Println("RES:")
	fmt.Println(string(rj))
	fmt.Println()

	assert.Fail(t, "FASF")
}

func TestMain(m *testing.M) {
	os.Exit(func() int {
		ctx := context.Background()

		compose, err := tc.NewDockerCompose("testdata/docker-compose.yaml")
		if err != nil {
			panic(err)
		}

		defer func() {
			compose.Down(context.Background(), tc.RemoveOrphans(true), tc.RemoveImagesLocal)
		}()

		err = compose.
			WaitForService("redpanda-connect", wait.NewHTTPStrategy("/").WithPort("8083/tcp").
				WithPollInterval(500*time.Millisecond).
				WithStartupTimeout(9*time.Minute)).
			Up(ctx, tc.Wait(true))

		if err != nil {
			panic(err)
		}

		testSeedBrokers = []string{"localhost:9092"}

		// create a long lived stock test topic
		kafkaCl, err := kgo.NewClient(
			kgo.SeedBrokers(testSeedBrokers...),
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
