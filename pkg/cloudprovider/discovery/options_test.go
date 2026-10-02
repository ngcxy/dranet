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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSplitProviderOptions(t *testing.T) {
	tests := []struct {
		name    string
		pairs   []string
		want    map[string]string
		wantErr bool
	}{
		{name: "no flag", want: map[string]string{}},
		{name: "single pair", pairs: []string{"oke.rdma-child-ipv4-cidr=10.192.0.0/15"}, want: map[string]string{"oke.rdma-child-ipv4-cidr": "10.192.0.0/15"}},
		{name: "one pair per occurrence", pairs: []string{"oke.a=1", "cks.b=2"}, want: map[string]string{"oke.a": "1", "cks.b": "2"}},
		{name: "value with commas", pairs: []string{"oke.a=10.0.0.0/8,10.1.0.0/16"}, want: map[string]string{"oke.a": "10.0.0.0/8,10.1.0.0/16"}},
		{name: "comma-separated pairs are one value", pairs: []string{"oke.a=1,oke.b=2"}, want: map[string]string{"oke.a": "1,oke.b=2"}},
		{name: "value with equals sign", pairs: []string{"oke.a=x=y"}, want: map[string]string{"oke.a": "x=y"}},
		{name: "spaces around keys and values are trimmed", pairs: []string{" oke.a = 1 ", "oke.b=2"}, want: map[string]string{"oke.a": "1", "oke.b": "2"}},
		{name: "empty occurrences are skipped", pairs: []string{"", "oke.a=1", ""}, want: map[string]string{"oke.a": "1"}},
		{name: "empty value is kept for the provider to check", pairs: []string{"oke.a="}, want: map[string]string{"oke.a": ""}},
		{name: "missing equals sign", pairs: []string{"oke.rdma-child-ipv4-cidr"}, wantErr: true},
		{name: "missing period", pairs: []string{"childcidr=10.192.0.0/15"}, wantErr: true},
		{name: "empty provider", pairs: []string{".rdma-child-ipv4-cidr=x"}, wantErr: true},
		{name: "empty option name", pairs: []string{"oke.=x"}, wantErr: true},
		{name: "duplicate key across occurrences", pairs: []string{"oke.a=1", "oke.a=2"}, wantErr: true},
		{name: "duplicate key after trimming", pairs: []string{"oke.a=1", " oke.a=2"}, wantErr: true},
		{name: "space-only occurrence", pairs: []string{"oke.a=1", " "}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitProviderOptions(tt.pairs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("splitProviderOptions(%q) returned no error, got %#v", tt.pairs, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitProviderOptions(%q) returned error: %v", tt.pairs, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("splitProviderOptions(%q) mismatch (-want +got):\n%s", tt.pairs, diff)
			}
		})
	}
}

func TestParseProviderOptions(t *testing.T) {
	tests := []struct {
		name    string
		pairs   []string
		want    ProviderOptions
		wantErr string
	}{
		{name: "no options", want: ProviderOptions{}},
		{name: "only empty occurrences", pairs: []string{"", ""}, want: ProviderOptions{}},
		{name: "a syntax error stops parsing", pairs: []string{"oke.a"}, wantErr: "not a key=value pair"},
		{name: "valid OKE option has a bare name", pairs: []string{"oke.rdma-child-ipv4-cidr=10.192.0.0/14"}, want: ProviderOptions{CloudProviderHintOKE: {"rdma-child-ipv4-cidr": "10.192.0.0/14"}}},
		{name: "bad OKE value", pairs: []string{"oke.rdma-child-ipv4-cidr=10.192.0.0/33"}, wantErr: "oke options: option rdma-child-ipv4-cidr: "},
		{name: "empty OKE value", pairs: []string{"oke.rdma-child-ipv4-cidr="}, wantErr: "oke options: option rdma-child-ipv4-cidr: "},
		{name: "unknown OKE key", pairs: []string{"oke.unknown=1"}, wantErr: `oke options: unknown option "unknown"`},
		{name: "CKS defines no options yet", pairs: []string{"cks.a=1"}, wantErr: `provider cks defines no options, got "cks.a"`},
		{name: "a typo in the provider fails", pairs: []string{"okee.a=1"}, wantErr: `provider okee defines no options, got "okee.a"`},
		{name: "an upper-case provider fails", pairs: []string{"OKE.rdma-child-ipv4-cidr=10.192.0.0/14"}, wantErr: `provider OKE defines no options, got "OKE.rdma-child-ipv4-cidr"`},
		{name: "a valid OKE option does not hide another provider", pairs: []string{"oke.rdma-child-ipv4-cidr=10.192.0.0/14", "gce.a=1"}, wantErr: `provider gce defines no options, got "gce.a"`},
		{name: "the error names the first unknown provider", pairs: []string{"zz.a=1", "gce.a=1", "cks.a=1"}, wantErr: `provider cks defines no options, got "cks.a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProviderOptions(tt.pairs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseProviderOptions(%q) error = %v, want it to contain %q", tt.pairs, err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("ParseProviderOptions(%q) returned %v with an error", tt.pairs, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProviderOptions(%q) error = %v, want nil", tt.pairs, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseProviderOptions(%q) mismatch (-want +got):\n%s", tt.pairs, diff)
			}
		})
	}
}

// The first unknown provider in sorted order names the error. Ten providers
// and 20 runs make a wrong map order show.
func TestParseProviderOptionsNamesTheFirstUnknownProvider(t *testing.T) {
	var pairs []string
	for _, hint := range testHints() {
		pairs = append(pairs, namespace(hint)+".a=1")
	}
	want := `provider p0 defines no options, got "p0.a"`
	for range 20 {
		if _, err := ParseProviderOptions(pairs); err == nil || err.Error() != want {
			t.Fatalf("ParseProviderOptions(%q) error = %v, want %q", pairs, err, want)
		}
	}
}

// testHints returns ten test-only providers P0 to P9, in sorted order. With
// ten keys, a map order almost never matches the sorted order by chance.
func testHints() []CloudProviderHint {
	var hints []CloudProviderHint
	for i := range 10 {
		hints = append(hints, CloudProviderHint(fmt.Sprintf("P%d", i)))
	}
	return hints
}

// The OKE option rejects commas, so test-only providers cover the generic
// parts: several providers, values with commas, and the order of the checks.
func TestParseProviderOptionsWithTestValidators(t *testing.T) {
	var checked []CloudProviderHint
	original := optionValidators
	t.Cleanup(func() { optionValidators = original })
	optionValidators = map[CloudProviderHint]func(map[string]string) error{
		"BAD": func(map[string]string) error { return errors.New("bad value") },
	}
	var pairs []string
	want := ProviderOptions{}
	for _, hint := range testHints() {
		optionValidators[hint] = func(map[string]string) error {
			checked = append(checked, hint)
			return nil
		}
		pairs = append(pairs, namespace(hint)+".a=1")
		want[hint] = map[string]string{"a": "1"}
	}
	pairs = append(pairs, "p3.ranges=10.0.0.0/8,10.1.0.0/16")
	want["P3"]["ranges"] = "10.0.0.0/8,10.1.0.0/16"

	got, err := ParseProviderOptions(pairs)
	if err != nil {
		t.Fatalf("ParseProviderOptions() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseProviderOptions() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(testHints(), checked); diff != "" {
		t.Errorf("providers checked in the wrong order (-want +got):\n%s", diff)
	}

	_, err = ParseProviderOptions([]string{"bad.a=1"})
	if want := "bad options: bad value"; err == nil || err.Error() != want {
		t.Errorf("ParseProviderOptions() error = %v, want %q", err, want)
	}
}

func TestCheckHint(t *testing.T) {
	okeOptions := map[string]string{"rdma-child-ipv4-cidr": "10.192.0.0/14"}
	manyProviders := ProviderOptions{CloudProviderHintOKE: okeOptions}
	for _, hint := range testHints() {
		manyProviders[hint] = map[string]string{"a": "1"}
	}
	tests := []struct {
		name    string
		options ProviderOptions
		hint    CloudProviderHint
		wantErr string
	}{
		{name: "no options", options: ProviderOptions{}, hint: CloudProviderHintNone},
		{name: "options of the hinted provider", options: ProviderOptions{CloudProviderHintOKE: okeOptions}, hint: CloudProviderHintOKE},
		{name: "another provider", options: ProviderOptions{CloudProviderHintOKE: okeOptions}, hint: CloudProviderHintGCE, wantErr: `--cloud-provider-options sets oke.* options, but --cloud-provider-hint is "GCE"`},
		{name: "hint NONE", options: ProviderOptions{CloudProviderHintOKE: okeOptions}, hint: CloudProviderHintNone, wantErr: `--cloud-provider-options sets oke.* options, but --cloud-provider-hint is "NONE"`},
		{name: "hint webhook", options: ProviderOptions{CloudProviderHintOKE: okeOptions}, hint: CloudProviderHintWebhook, wantErr: `--cloud-provider-options sets oke.* options, but --cloud-provider-hint is "webhook"`},
		{name: "the error names the first other provider", options: manyProviders, hint: CloudProviderHintOKE, wantErr: `sets p0.* options`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A map order changes on every loop, so repeat to show the same error.
			for range 20 {
				err := tt.options.CheckHint(tt.hint)
				if tt.wantErr == "" {
					if err != nil {
						t.Fatalf("CheckHint(%q) error = %v, want nil", tt.hint, err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CheckHint(%q) error = %v, want it to contain %q", tt.hint, err, tt.wantErr)
				}
			}
		})
	}
}
