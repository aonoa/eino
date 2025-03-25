/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package compose

func pregelChannelBuilder(_ []string, _ []string, _ func() any, _ func() streamReader) channel {
	return &pregelChannel{Values: make(map[string]any)}
}

type pregelChannel struct {
	Values map[string]any
}

func (ch *pregelChannel) convertValues(fn func(map[string]any) error) error {
	return fn(ch.Values)
}

func (ch *pregelChannel) reportValues(ins map[string]any) error {
	for k, v := range ins {
		ch.Values[k] = v
	}
	return nil
}

func (ch *pregelChannel) get(_ bool) (any, bool, error) {
	if len(ch.Values) == 0 {
		return nil, false, nil
	}
	defer func() { ch.Values = map[string]any{} }()
	values := make([]any, 0, len(ch.Values))
	for _, v := range ch.Values {
		values = append(values, v)
	}

	if len(values) == 1 {
		return values[0], true, nil
	}

	// merge
	v, err := mergeValues(values)
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (ch *pregelChannel) reportSkip(_ []string) bool {
	return false
}
func (ch *pregelChannel) reportDependencies(_ []string) {
	return
}
