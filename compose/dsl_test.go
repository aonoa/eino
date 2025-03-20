package compose

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/internal/generic"
	"github.com/cloudwego/eino/schema"
)

func TestGraphWithPassthrough(t *testing.T) {
	dsl := &GraphDSL{
		ID:        "test",
		Namespace: "test",
		Name:      generic.PtrOf("test_passthrough"),
		InputType: "map[string]any",
		Nodes: []*NodeDSL{
			{
				Key:       "1",
				ImplID:    "Passthrough",
				InputKey:  generic.PtrOf("1"),
				OutputKey: generic.PtrOf("1"),
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}

	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	assert.NoError(t, err)

	out, err := c.Invoke(ctx, `{"1": "hello"}`)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"1": "hello"}, out)
}

func TestInstantiation(t *testing.T) {
	msgType := reflect.TypeOf(&schema.Message{})
	msg := &schema.Message{
		Role:    schema.User,
		Content: "hello",
	}
	marshalled, _ := sonic.Marshal(msg)

	msgValue1 := newInstanceByType(msgType).Interface()
	_ = sonic.Unmarshal(marshalled, msgValue1)
	assert.Equal(t, msg, msgValue1)

	msg2 := &schema.Message{
		Role:    schema.System,
		Content: "world",
	}
	marshalled, _ = sonic.Marshal(msg2)
	msgValue2 := newInstanceByType(msgType).Interface()
	_ = sonic.Unmarshal(marshalled, msgValue2)
	assert.Equal(t, msg2, msgValue2)

	formatType := reflect.TypeOf(schema.FString)
	marshalled, _ = sonic.Marshal(schema.FString)
	formatValue := newInstanceByType(formatType).Interface()
	_ = sonic.Unmarshal(marshalled, formatValue)
	assert.Equal(t, schema.FString, formatValue)

	factoryFunc := reflect.ValueOf(prompt.FromMessages)
	result := factoryFunc.Call([]reflect.Value{
		reflect.ValueOf(formatValue),
		reflect.ValueOf(msgValue2),
		reflect.ValueOf(msgValue1)})
	template := result[0]

	g := NewGraph[any, any]()

	addPromptFn := reflect.ValueOf((*graph).AddChatTemplateNode)
	result = addPromptFn.Call([]reflect.Value{
		reflect.ValueOf(g.graph),
		reflect.ValueOf("prompt_node"),
		template,
		reflect.ValueOf(WithNodeName("prompt_node_name")),
	})
	//assert.NoError(t, result[0].Interface().(error))

	_ = g.AddEdge(START, "prompt_node")
	_ = g.AddEdge("prompt_node", END)
	ctx := context.Background()
	r, err := g.Compile(ctx)
	assert.NoError(t, err)
	out, err := r.Invoke(ctx, map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, []*schema.Message{
		{
			Role:    schema.System,
			Content: "world",
		},
		{
			Role:    schema.User,
			Content: "hello",
		},
	}, out)
}

func TestGraphWithChatTemplate(t *testing.T) {
	dsl := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_chat_template"),
		InputType:       "map[string]any",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		Nodes: []*NodeDSL{
			{
				Key:    "1",
				ImplID: "prompt.DefaultChatTemplate",
				Configs: []Config{
					{
						Value: 0,
					},
					{
						Slot: &Slot{
							TypeID: "schema.messagePlaceholder",
							Configs: []Config{
								{
									Value: "history",
								},
								{
									Value: true,
								},
							},
						},
					},
					{
						Slot: &Slot{
							TypeID: "*schema.Message",
							Configs: []Config{
								{
									Value: `{"role":"system", "content":"you are a {certain} assistant"}`,
								},
							},
						},
					},
					{
						Slot: &Slot{
							TypeID: "*schema.Message",
							Configs: []Config{
								{
									Value: `{"role":"user", "content":"hello, {world}"}`,
								},
							},
						},
					},
				},
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}

	content, err := yaml.Marshal(dsl)
	assert.NoError(t, err)
	t.Log(string(content))

	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Invoke(ctx, `{"world": "awesome", "certain": "shy"}`)
	assert.NoError(t, err)
	assert.Equal(t, []*schema.Message{
		{
			Role:    "system",
			Content: "you are a shy assistant",
		},
		{
			Role:    "user",
			Content: "hello, awesome",
		},
	}, out)
}

func TestGraphWithRetriever(t *testing.T) {
	implMap["testRetriever"] = testRetrieverImpl
	typeMap[testRetrieverType.ID] = testRetrieverType
	typeMap[testEmbeddingType.ID] = testEmbeddingType
	typeMap[testRetrieverConfigType.ID] = testRetrieverConfigType
	typeMap[testEmbeddingConfigType.ID] = testEmbeddingConfigType
	defer func() {
		delete(implMap, "testRetriever")
		delete(typeMap, testRetrieverType.ID)
		delete(typeMap, testEmbeddingType.ID)
		delete(typeMap, testRetrieverConfigType.ID)
		delete(typeMap, testEmbeddingConfigType.ID)
	}()

	dsl := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_retriever"),
		InputType:       "string",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		Nodes: []*NodeDSL{
			{
				Key:    "1",
				ImplID: "testRetriever",
				Configs: []Config{
					{
						Slots: []Slot{
							{
								TypeID: "testEmbedding",
								Path:   FieldPath{"Embedding"},
								Configs: []Config{
									{},
								},
							},
						},
					},
				},
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}

	content, err := yaml.Marshal(dsl)
	assert.NoError(t, err)
	t.Log(string(content))

	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Invoke(ctx, "hello")
	assert.NoError(t, err)
	assert.Equal(t, []*schema.Document{
		{
			ID:      "1",
			Content: "hello",
		},
	}, out)
}

func TestDSLWithLambda(t *testing.T) {
	dsl := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_lambda"),
		InputType:       "*schema.Message",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		Nodes: []*NodeDSL{
			{
				Key:    "1",
				ImplID: "lambda.MessagePtrToList",
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}

	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Invoke(ctx, `{"role": "user", "content": "hello"}`)
	assert.NoError(t, err)
	assert.Equal(t, []*schema.Message{
		{
			Role:    schema.User,
			Content: "hello",
		},
	}, out)
}

func TestDSLWithBranch(t *testing.T) {
	f := func(_ context.Context, sr *schema.StreamReader[*schema.Message]) (string, error) {
		defer sr.Close()

		for {
			msg, err := sr.Recv()
			if err != nil {
				return "", err
			}

			if len(msg.ToolCalls) > 0 {
				return "1", nil
			}

			if len(msg.Content) == 0 { // skip empty chunks at the front
				continue
			}

			return "2", nil
		}
	}

	branchFunctionMap["firstChunkStreamToolCallChecker"] = &BranchFunction{
		ID:              "firstChunkStreamToolCallChecker",
		FuncValue:       reflect.ValueOf(f),
		InputType:       reflect.TypeOf(&schema.Message{}),
		IsStream:        true,
		StreamConverter: &StreamConverterImpl[*schema.Message]{},
	}
	defer func() {
		delete(branchFunctionMap, "firstChunkStreamToolCallChecker")
	}()

	dsl := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_branch"),
		InputType:       "*schema.Message",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		Nodes: []*NodeDSL{
			{
				Key:    "1",
				ImplID: "lambda.MessagePtrToList",
				Name:   generic.PtrOf("1"),
			},
			{
				Key:    "2",
				ImplID: "lambda.MessagePtrToList",
				Name:   generic.PtrOf("2"),
			},
		},
		Edges: []*EdgeDSL{
			{
				From: "1",
				To:   END,
			},
			{
				From: "2",
				To:   END,
			},
		},
		Branches: []*GraphBranchDSL{
			{
				BranchDSL: &BranchDSL{
					Condition: "firstChunkStreamToolCallChecker",
					EndNodes:  []string{"1", "2"},
				},
				FromNode: START,
			},
		},
	}
	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	if err != nil {
		t.Fatal(err)
	}

	var lambda1Cnt, lambda2Cnt int
	cbHandler := callbacks.NewHandlerBuilder().OnStartWithStreamInputFn(func(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
		if info.Name == "1" {
			lambda1Cnt++
		} else if info.Name == "2" {
			lambda2Cnt++
		}
		return ctx
	}).Build()
	sr, err := c.Transform(context.Background(), `{"role": "assistant", "tool_calls": [{"id": "1"}]}`, WithCallbacks(cbHandler))
	assert.NoError(t, err)
	assert.Equal(t, 1, lambda1Cnt)
	assert.Equal(t, 0, lambda2Cnt)
	sr.Close()
}

func TestDSLWithStateHandlers(t *testing.T) {
	type state struct {
		Cnt int
	}

	stateType := &TypeMeta{
		ID:          "state",
		BasicType:   BasicTypeStruct,
		IsPtr:       true,
		ReflectType: generic.PtrOf(reflect.TypeOf(&state{})),
	}

	typeMap["state"] = stateType

	var globalPre, globalPost atomic.Int32
	preHandler := func(ctx context.Context, in *schema.Message, s *state) (*schema.Message, error) {
		s.Cnt++
		globalPre.Add(1)
		return in, nil
	}

	postHandler := func(ctx context.Context, in []*schema.Message, s *state) ([]*schema.Message, error) {
		s.Cnt++
		globalPost.Add(1)
		return in, nil
	}

	preHandlerType := &StateHandler{
		ID:        "preHandler",
		FuncValue: reflect.ValueOf(preHandler),
		InputType: reflect.TypeOf((*schema.Message)(nil)),
		StateType: reflect.TypeOf((*state)(nil)),
	}

	stateHandlerMap["preHandler"] = preHandlerType

	postHandlerType := &StateHandler{
		ID:        "postHandler",
		FuncValue: reflect.ValueOf(postHandler),
		InputType: reflect.TypeOf(([]*schema.Message)(nil)),
		StateType: reflect.TypeOf((*state)(nil)),
	}

	stateHandlerMap["postHandler"] = postHandlerType

	l := func(ctx context.Context, in *schema.Message) ([]*schema.Message, error) {
		err := ProcessState(ctx, func(_ context.Context, s *state) error {
			if s.Cnt != 1 {
				return errors.New("state cnt should be 1")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return []*schema.Message{in}, nil
	}

	implMap["lambda.l"] = &ImplMeta{
		TypeID:        "lambda.l",
		ComponentType: ComponentOfLambda,
		Lambda: func() *Lambda {
			return InvokableLambda(l)
		},
	}

	defer func() {
		delete(typeMap, "state")
		delete(stateHandlerMap, "preHandler")
		delete(stateHandlerMap, "postHandler")
		delete(implMap, "lambda.l")
	}()

	dsl := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_state_handlers"),
		InputType:       "*schema.Message",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		StateType:       (*TypeID)(generic.PtrOf("state")),
		Nodes: []*NodeDSL{
			{
				Key:              "1",
				ImplID:           "lambda.l",
				StatePreHandler:  (*StateHandlerID)(generic.PtrOf("preHandler")),
				StatePostHandler: (*StateHandlerID)(generic.PtrOf("postHandler")),
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}
	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Invoke(ctx, `{"role": "user", "content": "hello"}`)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), globalPre.Load())
	assert.Equal(t, int32(1), globalPost.Load())
}

func TestDSLWithStreamStateHandlers(t *testing.T) {
	type state struct {
		Cnt int
	}

	stateType := &TypeMeta{
		ID:          "state",
		BasicType:   BasicTypeStruct,
		IsPtr:       true,
		ReflectType: generic.PtrOf(reflect.TypeOf(&state{})),
	}

	typeMap["state"] = stateType

	var globalPre, globalPost atomic.Int32
	preHandler := func(ctx context.Context, in *schema.StreamReader[*schema.Message], s *state) (*schema.StreamReader[*schema.Message], error) {
		s.Cnt++
		globalPre.Add(1)
		return in, nil
	}

	postHandler := func(ctx context.Context, in *schema.StreamReader[[]*schema.Message], s *state) (*schema.StreamReader[[]*schema.Message], error) {
		s.Cnt++
		globalPost.Add(1)
		return in, nil
	}

	preHandlerType := &StateHandler{
		ID:              "preHandler",
		FuncValue:       reflect.ValueOf(preHandler),
		InputType:       reflect.TypeOf((*schema.Message)(nil)),
		StateType:       reflect.TypeOf((*state)(nil)),
		IsStream:        true,
		StreamConverter: &StreamConverterImpl[*schema.Message]{},
	}

	stateHandlerMap["preHandler"] = preHandlerType

	postHandlerType := &StateHandler{
		ID:              "postHandler",
		FuncValue:       reflect.ValueOf(postHandler),
		InputType:       reflect.TypeOf(([]*schema.Message)(nil)),
		StateType:       reflect.TypeOf((*state)(nil)),
		IsStream:        true,
		StreamConverter: &StreamConverterImpl[[]*schema.Message]{},
	}

	stateHandlerMap["postHandler"] = postHandlerType

	defer func() {
		delete(typeMap, "state")
		delete(stateHandlerMap, "preHandler")
		delete(stateHandlerMap, "postHandler")
	}()

	dsl := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_state_handlers"),
		InputType:       "*schema.Message",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		StateType:       (*TypeID)(generic.PtrOf("state")),
		Nodes: []*NodeDSL{
			{
				Key:              "1",
				ImplID:           "lambda.MessagePtrToList",
				StatePreHandler:  (*StateHandlerID)(generic.PtrOf("preHandler")),
				StatePostHandler: (*StateHandlerID)(generic.PtrOf("postHandler")),
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}
	ctx := context.Background()
	c, err := CompileGraph(ctx, dsl)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Invoke(ctx, `{"role": "user", "content": "hello"}`)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), globalPre.Load())
	assert.Equal(t, int32(1), globalPost.Load())
}

func TestDSLWithSubGraph(t *testing.T) {
	subGraphDSL := &GraphDSL{
		ID:              "test_sub",
		Namespace:       "test_sub",
		Name:            generic.PtrOf("test_sub_graph"),
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		Nodes: []*NodeDSL{
			{
				Key:    "1",
				ImplID: "lambda.MessagePtrToList",
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}

	parentGraphDSL := &GraphDSL{
		ID:              "test",
		Namespace:       "test",
		Name:            generic.PtrOf("test_parent_graph"),
		InputType:       "*schema.Message",
		NodeTriggerMode: generic.PtrOf(AllPredecessor),
		Nodes: []*NodeDSL{
			{
				Key:      "1",
				GraphDSL: subGraphDSL,
			},
		},
		Edges: []*EdgeDSL{
			{
				From: START,
				To:   "1",
			},
			{
				From: "1",
				To:   END,
			},
		},
	}

	content, err := yaml.Marshal(parentGraphDSL)
	assert.NoError(t, err)
	t.Log(string(content))

	ctx := context.Background()
	c, err := CompileGraph(ctx, parentGraphDSL)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Invoke(ctx, `{"role": "user", "content": "hello"}`)
	assert.NoError(t, err)
	assert.Equal(t, []*schema.Message{
		{
			Role:    schema.User,
			Content: "hello",
		},
	}, out)
}

type testRetriever struct{}

type testRetrieverConfig struct {
	Embedding embedding.Embedder
}

func NewTestRetriever(config *testRetrieverConfig) (retriever.Retriever, error) {
	if config == nil || config.Embedding == nil {
		return nil, errors.New("invalid config")
	}
	return &testRetriever{}, nil
}

func (t *testRetriever) Retrieve(_ context.Context, query string, _ ...retriever.Option) ([]*schema.Document, error) {
	return []*schema.Document{
		{
			ID:      "1",
			Content: query,
		},
	}, nil
}

type testEmbedding struct{}

type testEmbeddingConfig struct{}

func NewTestEmbedding(_ context.Context, config *testEmbeddingConfig) (embedding.Embedder, error) {
	if config == nil {
		return nil, errors.New("invalid config")
	}
	return &testEmbedding{}, nil
}

func (t *testEmbedding) EmbedStrings(_ context.Context, _ []string, _ ...embedding.Option) ([][]float64, error) {
	panic("implement me")
}

var testRetrieverType = &TypeMeta{
	ID:                "testRetriever",
	BasicType:         BasicTypeStruct,
	IsPtr:             true,
	InterfaceType:     (*TypeID)(generic.PtrOf("retriever.Retriever")),
	InstantiationType: InstantiationTypeFunction,
	FunctionMeta: &FunctionMeta{
		Name:      "NewTestRetriever",
		FuncValue: reflect.ValueOf(NewTestRetriever),
		InputTypes: []TypeID{
			"testRetrieverConfig",
		},
		OutputTypes: []TypeID{
			"retriever.Retriever",
			errType.ID,
		},
	},
}

var testRetrieverConfigType = &TypeMeta{
	ID:                "testRetrieverConfig",
	BasicType:         BasicTypeStruct,
	IsPtr:             true,
	InstantiationType: InstantiationTypeUnmarshal,
	ReflectType:       generic.PtrOf(reflect.TypeOf(&testRetrieverConfig{})),
}

var testEmbeddingType = &TypeMeta{
	ID:                "testEmbedding",
	BasicType:         BasicTypeStruct,
	IsPtr:             true,
	InterfaceType:     (*TypeID)(generic.PtrOf("embedding.Embedder")),
	InstantiationType: InstantiationTypeFunction,
	FunctionMeta: &FunctionMeta{
		Name:      "NewTestEmbedding",
		FuncValue: reflect.ValueOf(NewTestEmbedding),
		InputTypes: []TypeID{
			ctxType.ID,
			"testEmbeddingConfig",
		},
		OutputTypes: []TypeID{
			"embedding.Embedder",
			errType.ID,
		},
	},
}

var testEmbeddingConfigType = &TypeMeta{
	ID:                "testEmbeddingConfig",
	BasicType:         BasicTypeStruct,
	IsPtr:             true,
	InstantiationType: InstantiationTypeUnmarshal,
	ReflectType:       generic.PtrOf(reflect.TypeOf(&testEmbeddingConfig{})),
}

var testRetrieverImpl = &ImplMeta{
	TypeID:        "testRetriever",
	ComponentType: components.ComponentOfRetriever,
}
