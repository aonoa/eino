package compose

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cloudwego/eino/internal/generic"
)

func TestWorkflowFromDSL(t *testing.T) {
	lambda1 := func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["lambda1"] = "1"
		return in, nil
	}

	lambda2 := func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["lambda2"] = "2"
		return in, nil
	}

	lambda3 := func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["lambda3"] = "3"
		return in, nil
	}

	lambda4 := func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["lambda4"] = "4"
		return in, nil
	}

	lambda5 := func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["lambda5"] = "5"
		return in, nil
	}

	implMap["lambda1"] = &ImplMeta{
		ComponentType: ComponentOfLambda,
		Lambda:        func() *Lambda { return InvokableLambda(lambda1) },
	}

	implMap["lambda2"] = &ImplMeta{
		ComponentType: ComponentOfLambda,
		Lambda:        func() *Lambda { return InvokableLambda(lambda2) },
	}

	implMap["lambda3"] = &ImplMeta{
		ComponentType: ComponentOfLambda,
		Lambda:        func() *Lambda { return InvokableLambda(lambda3) },
	}

	implMap["lambda4"] = &ImplMeta{
		ComponentType: ComponentOfLambda,
		Lambda:        func() *Lambda { return InvokableLambda(lambda4) },
	}

	implMap["lambda5"] = &ImplMeta{
		ComponentType: ComponentOfLambda,
		Lambda:        func() *Lambda { return InvokableLambda(lambda5) },
	}

	condition := func(ctx context.Context, in map[string]any) (string, error) {
		if in[START] == "hello" {
			return "lambda4", nil
		}

		return "lambda3", nil
	}

	branchFunctionMap["condition"] = &BranchFunction{
		ID:        "condition",
		FuncValue: reflect.ValueOf(condition),
		InputType: reflect.TypeOf(map[string]any{}),
		IsStream:  false,
	}

	defer func() {
		delete(implMap, "lambda1")
		delete(implMap, "lambda2")
		delete(implMap, "lambda3")
		delete(implMap, "lambda4")
		delete(implMap, "lambda5")
		delete(branchFunctionMap, "condition")
	}()

	dsl := &WorkflowDSL{
		ID:         "test",
		Namespace:  "test",
		Name:       generic.PtrOf("test_workflow"),
		InputType:  "map[string]any",
		OutputType: "map[string]any",
		Nodes: []*WorkflowNodeDSL{
			{
				NodeDSL: &NodeDSL{
					Key:    "lambda1",
					ImplID: "lambda1",
				},
				Inputs: []*WorkflowNodeInputDSL{
					{
						FromNodeKey: START,
					},
				},
			},
			{
				NodeDSL: &NodeDSL{
					Key:    "lambda2",
					ImplID: "lambda2",
				},
				Inputs: []*WorkflowNodeInputDSL{
					{
						FromNodeKey: START,
					},
				},
			},
			{
				NodeDSL: &NodeDSL{
					Key:    "lambda3",
					ImplID: "lambda3",
				},
				Inputs: []*WorkflowNodeInputDSL{
					{
						FromNodeKey: "lambda1",
						FieldPathMappings: []FieldPathMapping{
							{
								From: FieldPath{"lambda1"},
								To:   FieldPath{"lambda1"},
							},
						},
						NoDirectDependency: true,
					},
				},
			},
			{
				NodeDSL: &NodeDSL{
					Key:    "lambda4",
					ImplID: "lambda4",
				},
				Inputs: []*WorkflowNodeInputDSL{
					{
						FromNodeKey: "lambda2",
						FieldPathMappings: []FieldPathMapping{
							{
								From: FieldPath{"lambda2"},
								To:   FieldPath{"lambda2"},
							},
						},
						NoDirectDependency: true,
					},
				},
			},
			{
				NodeDSL: &NodeDSL{
					Key:    "lambda5",
					ImplID: "lambda5",
				},
				Inputs: []*WorkflowNodeInputDSL{
					{
						FromNodeKey: "lambda1",
						FieldPathMappings: []FieldPathMapping{
							{
								From: FieldPath{"lambda1"},
								To:   FieldPath{"lambda1"},
							},
						},
						NoDirectDependency: true,
					},
				},
				Dependencies: []string{
					"lambda3",
				},
			},
		},
		Branches: []*WorkflowBranchDSL{
			{
				Key: "branch",
				BranchDSL: &BranchDSL{
					Condition: "condition",
					EndNodes: []string{
						"lambda3",
						"lambda4",
					},
				},
				Inputs: []*WorkflowNodeInputDSL{
					{
						FromNodeKey: "lambda1",
						FieldPathMappings: []FieldPathMapping{
							{
								From: FieldPath{"lambda1"},
								To:   FieldPath{"lambda1"},
							},
						},
					},
					{
						FromNodeKey: START,
						FieldPathMappings: []FieldPathMapping{
							{
								From: FieldPath{START},
								To:   FieldPath{START},
							},
						},
						NoDirectDependency: true,
					},
				},
				Dependencies: []string{
					"lambda2",
				},
			},
		},
		EndInputs: []*WorkflowNodeInputDSL{
			{
				FromNodeKey: "lambda4",
				FieldPathMappings: []FieldPathMapping{
					{
						From: FieldPath{"lambda4"},
						To:   FieldPath{"lambda4"},
					},
				},
			},
			{
				FromNodeKey: "lambda3",
				FieldPathMappings: []FieldPathMapping{
					{
						From: FieldPath{"lambda3"},
						To:   FieldPath{"lambda3"},
					},
				},
				NoDirectDependency: true,
			},
		},
		EndDependencies: []string{
			"lambda5",
		},
	}

	ctx := context.Background()
	r, err := CompileWorkflow(ctx, dsl)
	assert.NoError(t, err)
	out, err := r.Invoke(ctx, map[string]any{
		START: "hello",
	})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"lambda4": "4",
	}, out)

	out, err = r.Invoke(ctx, map[string]any{
		START: "hello1",
	})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"lambda3": "3",
	}, out)
}
