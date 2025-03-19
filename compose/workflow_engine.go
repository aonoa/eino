package compose

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/cloudwego/eino/components"
)

type CompiledGraph struct {
	run Runnable[any, any]
}

func InvokeCompiledGraph[In, Out any](ctx context.Context, g *CompiledGraph, input In, opts ...Option) (Out, error) {
	result, err := g.run.Invoke(ctx, input, opts...)
	if err != nil {
		var zero Out
		return zero, err
	}

	// Type assertion for the output
	out, ok := result.(Out)
	if !ok {
		return *new(Out), fmt.Errorf("failed to convert output to type %T", *new(Out))
	}

	return out, nil
}

func CompileGraph(ctx context.Context, dsl *GraphDSL) (*CompiledGraph, error) {
	newGraphOptions := make([]NewGraphOption, 0)
	if dsl.StateType != nil {
		stateFn := func(ctx context.Context) any {
			return dsl.StateType.InstantiateByLiteral()
		}
		newGraphOptions = append(newGraphOptions, WithGenLocalState(stateFn))
	}

	g := NewGraph[any, any](newGraphOptions...)
	for _, node := range dsl.Nodes {
		if err := graphAddNode(ctx, g.graph, node); err != nil {
			return nil, err
		}
	}

	for _, edge := range dsl.Edges {
		if err := g.AddEdge(edge.From, edge.To); err != nil {
			return nil, err
		}
	}

	compileOptions := make([]GraphCompileOption, 0)
	if dsl.NodeTriggerMode != nil {
		compileOptions = append(compileOptions, WithNodeTriggerMode(*dsl.NodeTriggerMode))
	}
	if dsl.MaxRunStep != nil {
		compileOptions = append(compileOptions, WithMaxRunSteps(*dsl.MaxRunStep))
	}
	if dsl.Name != nil {
		compileOptions = append(compileOptions, WithGraphName(*dsl.Name))
	}

	r, err := g.Compile(ctx, compileOptions...)
	if err != nil {
		return nil, err
	}

	return &CompiledGraph{
		run: r,
	}, nil
}

func graphAddNode(ctx context.Context, g *graph, dsl *NodeDSL) error {
	addNodeOpts := make([]GraphAddNodeOpt, 0)
	if dsl.Name != nil {
		addNodeOpts = append(addNodeOpts, WithNodeName(*dsl.Name))
	}
	if dsl.InputKey != nil {
		addNodeOpts = append(addNodeOpts, WithInputKey(*dsl.InputKey))
	}
	if dsl.OutputKey != nil {
		addNodeOpts = append(addNodeOpts, WithOutputKey(*dsl.OutputKey))
	}

	// TODO: state handlers

	implMeta, ok := implMap[dsl.ImplID]
	if !ok {
		return fmt.Errorf("impl not found: %v", dsl.ImplID)
	}

	if implMeta.ComponentType == ComponentOfPassthrough {
		return g.AddPassthroughNode(dsl.Key, addNodeOpts...)
	}

	typeMeta, ok := typeMap[implMeta.TypeID]
	if !ok {
		return fmt.Errorf("type not found: %v", implMeta.TypeID)
	}

	switch implMeta.ComponentType {
	case components.ComponentOfChatModel:
	case components.ComponentOfPrompt:
		result, err := typeMeta.Instantiate(ctx, dsl.Config, dsl.Configs, dsl.Slots)
		if err != nil {
			return err
		}

		addFn, ok := comp2AddFn[implMeta.ComponentType]
		if !ok {
			return fmt.Errorf("add node function not found: %v", implMeta.ComponentType)
		}
		results := addFn.Call(append([]reflect.Value{reflect.ValueOf(g), reflect.ValueOf(dsl.Key), result}))
		if len(results) != 1 {
			return fmt.Errorf("add node function return value length mismatch: given %d, defined %d", len(results), 1)
		}
		if results[0].Interface() != nil {
			return results[0].Interface().(error)
		}
	case components.ComponentOfRetriever:
	case components.ComponentOfIndexer:
	case components.ComponentOfEmbedding:
	case components.ComponentOfTransformer:
	case components.ComponentOfLoader:
	case components.ComponentOfTool:
	case ComponentOfToolsNode:
	case ComponentOfGraph:
	case ComponentOfChain:
	case ComponentOfLambda:
	case ComponentOfWorkflow:
	default:
		return fmt.Errorf("unsupported component type: %v", implMeta.ComponentType)
	}

	return nil
}

func assignSlot(destValue reflect.Value, taken any, toPaths FieldPath) (reflect.Value, error) {
	if len(toPaths) == 0 {
		return reflect.Value{}, fmt.Errorf("empty fieldPath")
	}

	var (
		originalDestValue = destValue
		parentMap         reflect.Value
		parentKey         string
	)

	for {
		path := toPaths[0]
		toPaths = toPaths[1:]
		if len(toPaths) == 0 {
			toSet := reflect.ValueOf(taken)

			if destValue.Kind() == reflect.Map {
				key, err := checkAndExtractToMapKey(path, destValue, toSet)
				if err != nil {
					return reflect.Value{}, err
				}

				destValue.SetMapIndex(key, toSet)

				if parentMap.IsValid() {
					parentMap.SetMapIndex(reflect.ValueOf(parentKey), destValue)
				}

				return originalDestValue, nil
			}

			if destValue.Kind() == reflect.Slice {
				instantiateIfNeeded(destValue)

				elem, err := checkAndExtractToSliceIndex(path, destValue)
				if err != nil {
					return reflect.Value{}, err
				}

				if !toSet.Type().AssignableTo(elem.Type()) {
					return reflect.Value{}, fmt.Errorf("field mapping to a slice element has a mismatched type. from=%v, to=%v", toSet.Type(), destValue.Type().Elem())
				}

				elem.Set(toSet)

				if parentMap.IsValid() {
					parentMap.SetMapIndex(reflect.ValueOf(parentKey), destValue)
				}

				return originalDestValue, nil
			}

			field, err := checkAndExtractToField(path, destValue, toSet)
			if err != nil {
				return reflect.Value{}, err
			}

			field.Set(toSet)

			if parentMap.IsValid() {
				parentMap.SetMapIndex(reflect.ValueOf(parentKey), destValue)
			}

			return originalDestValue, nil
		}

		if destValue.Kind() == reflect.Map {
			if !reflect.TypeOf(path).AssignableTo(destValue.Type().Key()) {
				return reflect.Value{}, fmt.Errorf("field mapping to a map key but output is not a map with string key, type=%v", destValue.Type())
			}

			keyValue := reflect.ValueOf(path)
			valueValue := destValue.MapIndex(keyValue)
			if !valueValue.IsValid() {
				valueValue = newInstanceByType(destValue.Type().Elem())
				destValue.SetMapIndex(keyValue, valueValue)
			}

			if parentMap.IsValid() {
				parentMap.SetMapIndex(reflect.ValueOf(parentKey), destValue)
			}

			parentMap = destValue
			parentKey = path
			destValue = valueValue

			continue
		}

		if destValue.Kind() == reflect.Slice {
			elem, err := checkAndExtractToSliceIndex(path, destValue)
			if err != nil {
				return reflect.Value{}, err
			}

			instantiateIfNeeded(elem)

			if parentMap.IsValid() {
				parentMap.SetMapIndex(reflect.ValueOf(parentKey), destValue)
			}

			parentMap = reflect.Value{}
			parentKey = ""
			destValue = elem

			continue
		}

		ptrValue := destValue
		for destValue.Kind() == reflect.Ptr {
			destValue = destValue.Elem()
		}

		if destValue.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("field mapping to a struct field but output is not a struct, type=%v", destValue.Type())
		}

		field := destValue.FieldByName(path)
		if !field.IsValid() {
			return reflect.Value{}, fmt.Errorf("field mapping to a struct field, but field not found. field=%v, outputType=%v", path, destValue.Type())
		}

		if !field.CanSet() {
			return reflect.Value{}, fmt.Errorf("field mapping to a struct field, but field not exported. field=%v, outputType=%v", path, destValue.Type())
		}

		instantiateIfNeeded(field)

		if parentMap.IsValid() {
			parentMap.SetMapIndex(reflect.ValueOf(parentKey), ptrValue)
		}

		parentMap = reflect.Value{}
		parentKey = ""

		destValue = field
	}
}

func (t *TypeMeta) InstantiateByUnmarshal(ctx context.Context, value string, slots []Slot) (reflect.Value, error) {
	rTypePtr := t.ReflectType
	if rTypePtr == nil {
		return reflect.Value{}, fmt.Errorf("reflect type not found: %v", t.ID)
	}
	rType := *rTypePtr
	isPtr := rType.Kind() == reflect.Ptr
	if !isPtr {
		rType = reflect.PointerTo(rType)
	}
	instance := newInstanceByType(rType).Interface()
	err := sonic.Unmarshal([]byte(value), instance)
	if err != nil {
		return reflect.Value{}, err
	}

	rValue := reflect.ValueOf(instance)
	if !isPtr {
		rValue = rValue.Elem()
	}

	return rValue, nil
}

func (t *TypeMeta) InstantiateByFunction(ctx context.Context, config *string, configs []Config, slots []Slot) (reflect.Value, error) {
	if config != nil && len(configs) > 0 {
		return reflect.Value{}, fmt.Errorf("config and configs cannot be both set")
	}

	fMeta := t.FunctionMeta
	if fMeta == nil {
		return reflect.Value{}, fmt.Errorf("function meta not found: %v", t.ID)
	}

	var (
		results          []reflect.Value
		nonCtxParamCount = len(fMeta.InputTypes)
	)
	if len(fMeta.InputTypes) == 0 {
		results = fMeta.FuncValue.Call([]reflect.Value{})
	} else {
		inputs := make(map[int]reflect.Value, len(configs)+1)
		first := fMeta.InputTypes[0]
		if first == ctxType.ID {
			nonCtxParamCount--
			inputs[0] = reflect.ValueOf(ctx)
			if config != nil {
				configs = []Config{{
					Index: 1,
					Value: *config,
				}}
			}
		} else {
			if config != nil {
				configs = []Config{
					{
						Index: 0,
						Value: *config,
					},
				}
			}
		}

		for _, conf := range configs {
			var fTypeID TypeID
			if conf.Index >= nonCtxParamCount {
				if !fMeta.IsVariadic {
					return reflect.Value{}, fmt.Errorf("configs length mismatch: given %d, nonCtxParamCount %d", len(configs), nonCtxParamCount)
				}
				fTypeID = fMeta.InputTypes[len(fMeta.InputTypes)-1]
			} else {
				fTypeID = fMeta.InputTypes[conf.Index]
			}

			fType, ok := typeMap[fTypeID]
			if !ok {
				return reflect.Value{}, fmt.Errorf("type not found: %v", fTypeID)
			}

			switch fType.InstantiationType {
			case InstantiationTypeUnmarshal:
				rValue, err := fType.InstantiateByUnmarshal(ctx, conf.Value, slots)
				if err != nil {
					return reflect.Value{}, err
				}
				if _, ok := inputs[conf.Index]; ok {
					return reflect.Value{}, fmt.Errorf("duplicate config index: %d", conf.Index)
				}

				inputs[conf.Index] = rValue
			default:
				return reflect.Value{}, fmt.Errorf("unsupported instantiation type for function parameter: %v", fType.InstantiationType)
			}
		}

		inputValues := make([]reflect.Value, len(inputs))
		for i, v := range inputs {
			if i >= len(inputs) {
				return reflect.Value{}, fmt.Errorf("index out of range: %d", i)
			}
			inputValues[i] = v
		}

		results = fMeta.FuncValue.Call(inputValues)
	}

	if len(results) != len(fMeta.OutputTypes) {
		return reflect.Value{}, fmt.Errorf("function return value length mismatch: given %d, defined %d", len(results), len(fMeta.OutputTypes))
	}

	if fMeta.OutputTypes[len(fMeta.OutputTypes)-1] == errType.ID {
		if results[len(results)-1].Interface() != nil {
			return reflect.Value{}, results[len(results)-1].Interface().(error)
		}
	}

	// TODO: validate first result type
	return results[0], nil
}

func (t *TypeMeta) Instantiate(ctx context.Context, config *string, configs []Config, slots []Slot) (reflect.Value, error) {
	switch t.BasicType {
	case BasicTypeInteger, BasicTypeString, BasicTypeBool, BasicTypeNumber:
		return reflect.ValueOf(t.InstantiateByLiteral()), nil
	case BasicTypeStruct, BasicTypeArray, BasicTypeMap:
		switch t.InstantiationType {
		case InstantiationTypeUnmarshal:
			if config == nil {
				return reflect.Value{}, fmt.Errorf("config is nil when instantiate by unmarshal: %v", t.ID)
			}
			return t.InstantiateByUnmarshal(ctx, *config, slots)
		case InstantiationTypeFunction:
			return t.InstantiateByFunction(ctx, config, configs, slots)
		default:
			return reflect.Value{}, fmt.Errorf("unsupported instantiation type %v, for basicType: %v, typeID: %s",
				t.InstantiationType, t.BasicType, t.ID)
		}
	default:
		return reflect.Value{}, fmt.Errorf("unsupported basic type: %v", t.BasicType)
	}
}

func (t *TypeMeta) InstantiateByLiteral() any {
	switch t.BasicType {
	case BasicTypeString:
		if t.IsPtr {
			return new(string)
		}
		return ""
	case BasicTypeInteger:
		if t.IntegerType == nil {
			panic(fmt.Sprintf("basic type is %v, integer type is nil", t.BasicType))
		}
		switch *t.IntegerType {
		case IntegerTypeInt8:
			if t.IsPtr {
				return new(int8)
			}
			return int8(0)
		case IntegerTypeInt16:
			if t.IsPtr {
				return new(int16)
			}
			return int16(0)
		case IntegerTypeInt32:
			if t.IsPtr {
				return new(int32)
			}
			return int32(0)
		case IntegerTypeInt64:
			if t.IsPtr {
				return new(int64)
			}
			return int64(0)
		case IntegerTypeUint8:
			if t.IsPtr {
				return new(uint8)
			}
			return uint8(0)
		case IntegerTypeUint16:
			if t.IsPtr {
				return new(uint16)
			}
			return uint16(0)
		case IntegerTypeUint32:
			if t.IsPtr {
				return new(uint32)
			}
			return uint32(0)
		case IntegerTypeUint64:
			if t.IsPtr {
				return new(uint64)
			}
			return uint64(0)
		case IntegerTypeInt:
			if t.IsPtr {
				return new(int)
			}
			return 0
		case IntegerTypeUInt:
			if t.IsPtr {
				return new(uint)
			}
			return uint(0)
		default:
			panic(fmt.Sprintf("unsupported integer type: %v", *t.IntegerType))
		}
	case BasicTypeNumber:
		if t.FloatType == nil {
			if t.IsPtr {
				return new(float64)
			}
			return float64(0)
		}
		switch *t.FloatType {
		case FloatTypeFloat32:
			if t.IsPtr {
				return new(float32)
			}
			return float32(0)
		case FloatTypeFloat64:
			if t.IsPtr {
				return new(float64)
			}
			return float64(0)
		default:
			panic(fmt.Sprintf("unsupported float type: %v", *t.FloatType))
		}
	case BasicTypeBool:
		if t.IsPtr {
			return new(bool)
		}
		return false
	default:
		panic(fmt.Sprintf("unsupported basic type: %s", t.BasicType))
	}
}

// checkAndExtractToSliceIndex extracts and validates the slice index from the path
func checkAndExtractToSliceIndex(path string, destValue reflect.Value) (reflect.Value, error) {
	// Extract the index part in the form of "[1]"
	startIndex := strings.Index(path, "[")
	endIndex := strings.Index(path, "]")
	if startIndex == -1 || endIndex == -1 || endIndex <= startIndex+1 {
		return reflect.Value{}, fmt.Errorf("invalid slice index syntax in path: %s", path)
	}
	indexStr := path[startIndex+1 : endIndex]

	// Parse the index from the extracted string
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("invalid slice index in path: %s", path)
	}

	// Check if the index is within the bounds of the slice
	if index < 0 {
		return reflect.Value{}, fmt.Errorf("slice index out of bounds: %d", index)
	}

	if index >= destValue.Len() {
		// Reslice destValue to contain enough elements
		newLen := index + 1
		capacity := destValue.Cap()
		if newLen > capacity {
			// If the new length exceeds the capacity, create a new slice
			newSlice := reflect.MakeSlice(destValue.Type(), newLen, newLen)
			reflect.Copy(newSlice, destValue)
			destValue.Set(newSlice)
		} else {
			// If the new length is within the capacity, just reslice
			destValue.SetLen(newLen)
		}
	}

	return destValue.Index(index), nil
}
