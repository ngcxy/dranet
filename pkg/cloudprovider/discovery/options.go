/*
Copyright The Kubernetes Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package discovery

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"sigs.k8s.io/dranet/pkg/cloudprovider/oke"
)

// ProviderOptions holds the checked --cloud-provider-options of every
// provider, keyed by hint. Option names carry no provider prefix.
type ProviderOptions map[CloudProviderHint]map[string]string

// For returns the options of one provider, or nil when it has none.
func (p ProviderOptions) For(hint CloudProviderHint) map[string]string { return p[hint] }

// optionValidators is the only place a provider registers for options. Its
// flag prefix is namespace(hint).
var optionValidators = map[CloudProviderHint]func(map[string]string) error{
	CloudProviderHintOKE: func(options map[string]string) error {
		_, err := oke.ParseOptions(options)
		return err
	},
}

// namespace is the flag prefix of a provider: OKE -> oke.
func namespace(hint CloudProviderHint) string { return strings.ToLower(string(hint)) }

func hintForNamespace(ns string) (CloudProviderHint, bool) {
	for hint := range optionValidators {
		if namespace(hint) == ns {
			return hint, true
		}
	}
	return "", false
}

// ParseProviderOptions parses <provider>.<option>=<value> pairs, one per flag
// occurrence, and checks them with the rules of their provider. The checks
// need no instance data, so a bad option stops DRANET before discovery.
func ParseProviderOptions(pairs []string) (ProviderOptions, error) {
	options, err := splitProviderOptions(pairs)
	if err != nil {
		return nil, err
	}
	result := ProviderOptions{}
	for _, key := range slices.Sorted(maps.Keys(options)) {
		ns, name, _ := strings.Cut(key, ".") // splitProviderOptions checked the format
		hint, ok := hintForNamespace(ns)
		if !ok {
			return nil, fmt.Errorf("provider %s defines no options, got %q", ns, key)
		}
		if result[hint] == nil {
			result[hint] = map[string]string{}
		}
		result[hint][name] = options[key]
	}
	for _, hint := range slices.Sorted(maps.Keys(result)) {
		if err := optionValidators[hint](result[hint]); err != nil {
			return nil, fmt.Errorf("%s options: %w", namespace(hint), err)
		}
	}
	return result, nil
}

// CheckHint returns an error when an explicit --cloud-provider-hint names a
// provider other than one that has options, because those options would
// configure nothing.
func (p ProviderOptions) CheckHint(hint CloudProviderHint) error {
	for _, other := range slices.Sorted(maps.Keys(p)) {
		if other != hint {
			return fmt.Errorf("--cloud-provider-options sets %s.* options, but --cloud-provider-hint is %q", namespace(other), hint)
		}
	}
	return nil
}

// splitProviderOptions splits each flag occurrence into a key and a value.
// Empty occurrences are skipped and spaces around keys and values are
// trimmed. Values may contain equals signs and commas.
func splitProviderOptions(pairs []string) (map[string]string, error) {
	options := map[string]string{}
	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		key, optionValue, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("cloud provider option %q is not a key=value pair", pair)
		}
		key, optionValue = strings.TrimSpace(key), strings.TrimSpace(optionValue)
		provider, name, found := strings.Cut(key, ".")
		if !found || provider == "" || name == "" {
			return nil, fmt.Errorf("cloud provider option key %q must use the <provider>.<option> format", key)
		}
		if _, exists := options[key]; exists {
			return nil, fmt.Errorf("duplicate cloud provider option %q", key)
		}
		options[key] = optionValue
	}
	return options, nil
}
