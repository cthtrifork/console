// Copyright 2022 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file https://github.com/redpanda-data/redpanda/blob/dev/licenses/bsl.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

//go:build integration

package console

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/redpanda-data/console/backend/pkg/config"
	"github.com/redpanda-data/console/backend/pkg/kafka"
	"github.com/redpanda-data/console/backend/pkg/kafka/mocks"
)

func Test_ListMessages(t *testing.T) {
	ctx := context.Background()
	log, err := zap.NewProduction()
	assert.NoError(t, err)

	// create test topic and test data in the real redpanda cluster
	_, kafkaAdmCl := createTestData(t, ctx, []string{testSeedBroker})

	// fake cluster
	fakeCluster, err := kfake.NewCluster(kfake.NumBrokers(1))
	assert.Nil(t, err)

	defer fakeCluster.Close()

	// create test topic and test data in the fake redpanda cluster
	_, _ = createTestData(t, ctx, fakeCluster.ListenAddrs())

	type test struct {
		name        string
		useFake     bool
		setup       func(context.Context)
		input       *ListMessageRequest
		expect      func(*mocks.MockIListMessagesProgress)
		expectError string
		cleanup     func(context.Context)
	}

	tests := []test{
		{
			name: "empty topic",
			setup: func(ctx context.Context) {
				_, err = kafkaAdmCl.CreateTopic(ctx, 1, 1, nil, "console_list_messages_empty_topic_test")
				assert.NoError(t, err)
			},
			input: &ListMessageRequest{
				TopicName:    "console_list_messages_empty_topic_test",
				PartitionID:  -1,
				StartOffset:  -2,
				MessageCount: 100,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnComplete(gomock.Any(), false)
			},
			cleanup: func(ctx context.Context) {
				kafkaAdmCl.DeleteTopics(ctx, "console_list_messages_empty_topic_test")
			},
		},
		{
			name: "all messages in a topic",
			input: &ListMessageRequest{
				TopicName:    "console_list_messages_topic_test",
				PartitionID:  -1,
				StartOffset:  -2,
				MessageCount: 100,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				var msg *kafka.TopicMessage
				var int64Type int64

				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnPhase("Consuming messages")
				mockProgress.EXPECT().OnMessage(gomock.AssignableToTypeOf(msg)).Times(20)
				mockProgress.EXPECT().OnMessageConsumed(gomock.AssignableToTypeOf(int64Type)).Times(20)
				mockProgress.EXPECT().OnComplete(gomock.AssignableToTypeOf(int64Type), false)
			},
		},
		{
			name: "single messages in a topic",
			input: &ListMessageRequest{
				TopicName:    "console_list_messages_topic_test",
				PartitionID:  -1,
				StartOffset:  10,
				MessageCount: 1,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				var int64Type int64

				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnPhase("Consuming messages")
				mockProgress.EXPECT().OnMessage(matchesOrder("10")).Times(1)
				mockProgress.EXPECT().OnMessageConsumed(gomock.AssignableToTypeOf(int64Type)).Times(1)
				mockProgress.EXPECT().OnComplete(gomock.AssignableToTypeOf(int64Type), false)
			},
		},
		{
			name: "five messages in a topic",
			input: &ListMessageRequest{
				TopicName:    "console_list_messages_topic_test",
				PartitionID:  -1,
				StartOffset:  10,
				MessageCount: 5,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				var int64Type int64

				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnPhase("Consuming messages")
				mockProgress.EXPECT().OnMessage(matchesOrder("10")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("11")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("12")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("13")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("14")).Times(1)
				mockProgress.EXPECT().OnMessageConsumed(gomock.AssignableToTypeOf(int64Type)).Times(5)
				mockProgress.EXPECT().OnComplete(gomock.AssignableToTypeOf(int64Type), false)
			},
		},
		{
			name: "time stamp in future get last record",
			input: &ListMessageRequest{
				TopicName:      "console_list_messages_topic_test",
				PartitionID:    -1,
				MessageCount:   5,
				StartTimestamp: time.Date(2010, time.November, 11, 13, 0, 0, 0, time.UTC).UnixMilli(),
				StartOffset:    StartOffsetTimestamp,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				var int64Type int64

				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnPhase("Consuming messages")
				mockProgress.EXPECT().OnMessage(matchesOrder("19")).Times(1)
				mockProgress.EXPECT().OnMessageConsumed(gomock.AssignableToTypeOf(int64Type)).Times(1)
				mockProgress.EXPECT().OnComplete(gomock.AssignableToTypeOf(int64Type), false)
			},
		},
		{
			name: "time stamp in middle get 5 records",
			input: &ListMessageRequest{
				TopicName:      "console_list_messages_topic_test",
				PartitionID:    -1,
				MessageCount:   5,
				StartTimestamp: time.Date(2010, time.November, 10, 13, 10, 30, 0, time.UTC).UnixMilli(),
				StartOffset:    StartOffsetTimestamp,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				var int64Type int64

				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnPhase("Consuming messages")
				mockProgress.EXPECT().OnMessage(matchesOrder("11")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("12")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("13")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("14")).Times(1)
				mockProgress.EXPECT().OnMessage(matchesOrder("15")).Times(1)
				mockProgress.EXPECT().OnMessageConsumed(gomock.AssignableToTypeOf(int64Type)).Times(5)
				mockProgress.EXPECT().OnComplete(gomock.AssignableToTypeOf(int64Type), false)
			},
		},
		{
			name: "unknown topic",
			input: &ListMessageRequest{
				TopicName:    "console_list_messages_topic_test_unknown_topic",
				PartitionID:  -1,
				StartOffset:  -2,
				MessageCount: 100,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				mockProgress.EXPECT().OnPhase("Get Partitions")
			},
			expectError: "failed to get partitions: UNKNOWN_TOPIC_OR_PARTITION: This server does not host this topic-partition.",
		},
		{
			name:    "single messages in a topic fake",
			useFake: true,
			input: &ListMessageRequest{
				TopicName:    "console_list_messages_topic_test",
				PartitionID:  -1,
				StartOffset:  10,
				MessageCount: 1,
			},
			expect: func(mockProgress *mocks.MockIListMessagesProgress) {
				var int64Type int64

				mockProgress.EXPECT().OnPhase("Get Partitions")
				mockProgress.EXPECT().OnPhase("Get Watermarks and calculate consuming requests")
				mockProgress.EXPECT().OnPhase("Consuming messages")
				mockProgress.EXPECT().OnMessage(matchesOrder("10")).Times(1)
				mockProgress.EXPECT().OnMessageConsumed(gomock.AssignableToTypeOf(int64Type)).Times(1)
				mockProgress.EXPECT().OnComplete(gomock.AssignableToTypeOf(int64Type), false)

				var fetchCalls int32

				fakeCluster.Control(func(req kmsg.Request) (kmsg.Response, error, bool) {
					fakeCluster.KeepControl()

					rj, _ := json.Marshal(req)
					fmt.Printf("!!! Got Request: %+T\n%+v\n\n", req, string(rj))

					switch v := req.(type) {
					case *kmsg.ApiVersionsRequest:
						return nil, nil, false
					case *kmsg.MetadataRequest:
						assert.Len(t, v.Topics, 1)
						assert.Equal(t, "console_list_messages_topic_test", *(v.Topics[0].Topic))

						return nil, nil, false
					case *kmsg.ListOffsetsRequest:
						loReq, ok := req.(*kmsg.ListOffsetsRequest)
						assert.True(t, ok, "request is not a list offset request: %+T", req)

						assert.Len(t, loReq.Topics, 1)
						assert.Equal(t, "console_list_messages_topic_test", loReq.Topics[0].Topic)

						assert.Len(t, loReq.Topics[0].Partitions, 1)
						assert.Equal(t, int32(1), loReq.Topics[0].Partitions[0].MaxNumOffsets)

						return nil, nil, false
					case *kmsg.FetchRequest:
						fetchReq, ok := req.(*kmsg.FetchRequest)
						assert.True(t, ok, "request is not a list offset request: %+T", req)

						assert.Len(t, fetchReq.Topics, 1)
						assert.Equal(t, "console_list_messages_topic_test", fetchReq.Topics[0].Topic)
						fmt.Println("FETCH CALLS:", atomic.LoadInt32(&fetchCalls))

						if atomic.LoadInt32(&fetchCalls) == 0 {
							atomic.StoreInt32(&fetchCalls, 1)

							assert.Len(t, fetchReq.Topics[0].Partitions, 1)
							assert.Equal(t, int64(10), fetchReq.Topics[0].Partitions[0].FetchOffset)
						} else if atomic.LoadInt32(&fetchCalls) == 1 {
							atomic.StoreInt32(&fetchCalls, 2)

							assert.Len(t, fetchReq.Topics[0].Partitions, 1)
							assert.Equal(t, int64(20), fetchReq.Topics[0].Partitions[0].FetchOffset)
						} else {
							assert.Fail(t, "unexpected call to fake fetch request")
						}

						return nil, nil, false
					default:
						assert.Fail(t, fmt.Sprintf("unexpected call to fake kafka request %+T", v))

						return nil, nil, false
					}
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			metricName := strings.ReplaceAll(tc.name, " ", "")

			cfg := config.Config{}
			cfg.SetDefaults()
			cfg.MetricsNamespace = metricName

			fmt.Println("REAL ADDRS:", []string{testSeedBroker})
			fmt.Println("FAKE ADDRS:", fakeCluster.ListenAddrs())

			if tc.useFake {
				fmt.Println()
				fmt.Println("=== USE FAKE ===")
				fmt.Println()
				cfg.Kafka.Brokers = fakeCluster.ListenAddrs()
			} else {
				cfg.Kafka.Brokers = []string{testSeedBroker}
			}

			kafkaSvc, err := kafka.NewService(&cfg, log, metricName)
			assert.NoError(t, err)

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockProgress := mocks.NewMockIListMessagesProgress(mockCtrl)

			if tc.setup != nil {
				tc.setup(ctx)
			}

			if tc.expect != nil {
				tc.expect(mockProgress)
			}

			svc, err := NewService(cfg.Console, log, kafkaSvc, nil, nil)
			assert.NoError(t, err)

			err = svc.ListMessages(ctx, *tc.input, mockProgress)
			if tc.expectError != "" {
				assert.Error(t, err)
				assert.Equal(t, tc.expectError, err.Error())
			} else {
				assert.NoError(t, err)
			}

			if tc.cleanup != nil {
				tc.cleanup(ctx)
			}
		})
	}
}

type Order struct {
	ID string
}

type orderMatcher struct {
	expectedID string
	actualID   string
	err        string
}

func (om *orderMatcher) Matches(x interface{}) bool {
	if m, ok := x.(*kafka.TopicMessage); ok {
		order := Order{}
		err := json.Unmarshal(m.Value.Payload.Payload, &order)
		if err != nil {
			om.err = fmt.Sprintf("marshal error: %s", err.Error())
			return false
		}

		om.actualID = order.ID

		return order.ID == om.expectedID
	}

	om.err = "value is not a TopicMessage"
	return false
}

func (m *orderMatcher) String() string {
	return fmt.Sprintf("has order ID %s expected order ID %s. err: %s", m.actualID, m.expectedID, m.err)
}

func matchesOrder(id string) gomock.Matcher {
	return &orderMatcher{expectedID: id}
}

func createTestData(t *testing.T, ctx context.Context, brokers []string) (*kgo.Client, *kadm.Client) {
	cfg := config.Config{}
	cfg.SetDefaults()
	cfg.MetricsNamespace = "console_list_messages"
	cfg.Kafka.Brokers = brokers

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Kafka.Brokers...),
		kgo.MaxVersions(kversion.V2_6_0()),
		kgo.FetchMaxBytes(5 * 1000 * 1000), // 5MB
		kgo.MaxConcurrentFetches(12),
		// We keep control records because we need to consume them in order to know whether the last message in a
		// a partition is worth waiting for or not (because it's a control record which we would never receive otherwise)
		kgo.KeepControlRecords(),
	}

	kClient, err := kgo.NewClient(opts...)
	kafkaAdmCl := kadm.NewClient(kClient)

	_, err = kafkaAdmCl.CreateTopic(ctx, 1, 1, nil, "console_list_messages_topic_test")
	assert.NoError(t, err)

	g := new(errgroup.Group)
	g.Go(func() error {
		produceOrders(t, ctx, kClient, "console_list_messages_topic_test")
		return nil
	})

	err = g.Wait()
	assert.NoError(t, err)

	return kClient, kafkaAdmCl
}

func produceOrders(t *testing.T, ctx context.Context, kafkaCl *kgo.Client, topic string) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)

	recordTimeStamp := time.Date(2010, time.November, 10, 13, 0, 0, 0, time.UTC)

	i := 0
	for i < 20 {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			order := Order{ID: strconv.Itoa(i)}
			serializedOrder, err := json.Marshal(order)
			require.NoError(t, err)

			r := &kgo.Record{
				Key:       []byte(order.ID),
				Value:     serializedOrder,
				Topic:     topic,
				Timestamp: recordTimeStamp,
			}
			results := kafkaCl.ProduceSync(ctx, r)
			require.NoError(t, results.FirstErr())

			fmt.Println("produced:", i)

			i++
			recordTimeStamp = recordTimeStamp.Add(1 * time.Minute)
		}
	}
}
