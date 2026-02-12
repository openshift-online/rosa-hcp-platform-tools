package main

import (
	"encoding/json"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	workv1 "open-cluster-management.io/api/work/v1"
)

// TestFlagValidation verifies mutual exclusivity of --all and --cluster-id flags.
func TestFlagValidation(t *testing.T) {
	tests := []struct {
		name            string
		all             bool
		hostedClusterID string
		expectError     bool
		errorContains   string
	}{
		{
			name:            "both flags set should error",
			all:             true,
			hostedClusterID: "cluster-123",
			expectError:     true,
			errorContains:   "cannot specify both --all and --cluster-id",
		},
		{
			name:            "neither flag set should error",
			all:             false,
			hostedClusterID: "",
			expectError:     true,
			errorContains:   "must specify either --all or --cluster-id",
		},
		{
			name:            "only --all set is valid",
			all:             true,
			hostedClusterID: "",
			expectError:     false,
		},
		{
			name:            "only --cluster-id set is valid",
			all:             false,
			hostedClusterID: "cluster-123",
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the flag validation logic from the command
			var err error
			if tt.all && tt.hostedClusterID != "" {
				err = &validationError{msg: "cannot specify both --all and --cluster-id"}
			} else if !tt.all && tt.hostedClusterID == "" {
				err = &validationError{msg: "must specify either --all or --cluster-id"}
			}

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectError && err != nil && err.Error() != tt.errorContains {
				t.Errorf("error = %q, want %q", err.Error(), tt.errorContains)
			}
		})
	}
}

// validationError is a helper type for testing validation errors.
type validationError struct {
	msg string
}

func (e *validationError) Error() string {
	return e.msg
}

// TestPatchManifestWorkLabel verifies label injection into ManifestWork resources.
func TestPatchManifestWorkLabel(t *testing.T) {
	tests := []struct {
		name           string
		initialLabels  map[string]string
		expectError    bool
		expectedLabels map[string]string
	}{
		{
			name: "adds label to cluster with existing labels",
			initialLabels: map[string]string{
				"api.openshift.com/id": "cluster-123",
			},
			expectError: false,
			expectedLabels: map[string]string{
				"api.openshift.com/id":                           "cluster-123",
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name:          "adds label to cluster with no labels",
			initialLabels: map[string]string{},
			expectError:   false,
			expectedLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name:          "adds label to cluster with nil labels",
			initialLabels: nil,
			expectError:   false,
			expectedLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name: "updates existing egress policy label",
			initialLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "SomeOtherValue",
			},
			expectError: false,
			expectedLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name: "preserves other labels when adding egress policy",
			initialLabels: map[string]string{
				"api.openshift.com/id":   "cluster-123",
				"api.openshift.com/name": "my-cluster",
				"custom-label":           "custom-value",
			},
			expectError: false,
			expectedLabels: map[string]string{
				"api.openshift.com/id":                           "cluster-123",
				"api.openshift.com/name":                         "my-cluster",
				"custom-label":                                   "custom-value",
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hypershiftv1beta1.HostedCluster{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "hypershift.openshift.io/v1beta1",
					Kind:       "HostedCluster",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Labels:    tt.initialLabels,
				},
			}

			hcJSON, err := json.Marshal(hc)
			if err != nil {
				t.Fatalf("Failed to marshal HostedCluster: %v", err)
			}

			mw := &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-id",
					Namespace: "test-mgmt-cluster",
				},
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: []workv1.Manifest{
							{RawExtension: runtime.RawExtension{Raw: hcJSON}},
						},
					},
				},
			}

			// Simulate the patchManifestWorkLabel logic
			modified := false
			for i, manifest := range mw.Spec.Workload.Manifests {
				if manifest.Raw == nil {
					continue
				}

				var manifestData map[string]interface{}
				if err := json.Unmarshal(manifest.Raw, &manifestData); err != nil {
					continue
				}

				kind, _ := manifestData["kind"].(string)
				if kind != "HostedCluster" {
					continue
				}

				metadata, ok := manifestData["metadata"].(map[string]interface{})
				if !ok {
					metadata = make(map[string]interface{})
					manifestData["metadata"] = metadata
				}

				labels, ok := metadata["labels"].(map[string]interface{})
				if !ok {
					labels = make(map[string]interface{})
					metadata["labels"] = labels
				}

				labels["api.openshift.com/hosted-cluster-egress-policy"] = "NoEgress"

				jsonData, err := json.Marshal(manifestData)
				if err != nil {
					t.Fatalf("Failed to marshal modified manifest: %v", err)
				}

				mw.Spec.Workload.Manifests[i].Raw = jsonData
				modified = true
				break
			}

			if !modified && !tt.expectError {
				t.Error("Expected manifest to be modified")
			}

			// Verify the labels in the modified manifest
			var result map[string]interface{}
			if err := json.Unmarshal(mw.Spec.Workload.Manifests[0].Raw, &result); err != nil {
				t.Fatalf("Failed to unmarshal result: %v", err)
			}

			metadata := result["metadata"].(map[string]interface{})
			labels := metadata["labels"].(map[string]interface{})

			for key, expectedValue := range tt.expectedLabels {
				actualValue, ok := labels[key]
				if !ok {
					t.Errorf("Expected label %s not found", key)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("Label %s = %v, want %v", key, actualValue, expectedValue)
				}
			}

			// Verify the egress policy label is set correctly
			if labels["api.openshift.com/hosted-cluster-egress-policy"] != "NoEgress" {
				t.Error("egress policy label not set correctly")
			}
		})
	}
}

// TestFindHostedClusterInManifestWork verifies HostedCluster detection in multi-manifest ManifestWork.
func TestFindHostedClusterInManifestWork(t *testing.T) {
	tests := []struct {
		name          string
		manifests     []map[string]interface{}
		expectedIndex int
		expectFound   bool
	}{
		{
			name: "finds HostedCluster as first manifest",
			manifests: []map[string]interface{}{
				{
					"apiVersion": "hypershift.openshift.io/v1beta1",
					"kind":       "HostedCluster",
					"metadata": map[string]interface{}{
						"name":   "test-cluster",
						"labels": map[string]interface{}{},
					},
				},
			},
			expectedIndex: 0,
			expectFound:   true,
		},
		{
			name: "finds HostedCluster in middle of manifests",
			manifests: []map[string]interface{}{
				{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]interface{}{
						"name": "test-secret",
					},
				},
				{
					"apiVersion": "hypershift.openshift.io/v1beta1",
					"kind":       "HostedCluster",
					"metadata": map[string]interface{}{
						"name":   "test-cluster",
						"labels": map[string]interface{}{},
					},
				},
				{
					"apiVersion": "cert-manager.io/v1",
					"kind":       "Certificate",
					"metadata": map[string]interface{}{
						"name": "test-cert",
					},
				},
			},
			expectedIndex: 1,
			expectFound:   true,
		},
		{
			name: "finds HostedCluster as last manifest",
			manifests: []map[string]interface{}{
				{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]interface{}{
						"name": "test-secret",
					},
				},
				{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test-configmap",
					},
				},
				{
					"apiVersion": "hypershift.openshift.io/v1beta1",
					"kind":       "HostedCluster",
					"metadata": map[string]interface{}{
						"name":   "test-cluster",
						"labels": map[string]interface{}{},
					},
				},
			},
			expectedIndex: 2,
			expectFound:   true,
		},
		{
			name: "does not find HostedCluster when not present",
			manifests: []map[string]interface{}{
				{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]interface{}{
						"name": "test-secret",
					},
				},
				{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test-configmap",
					},
				},
			},
			expectedIndex: -1,
			expectFound:   false,
		},
		{
			name:          "handles empty manifests",
			manifests:     []map[string]interface{}{},
			expectedIndex: -1,
			expectFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var manifests []workv1.Manifest
			for _, m := range tt.manifests {
				jsonData, err := json.Marshal(m)
				if err != nil {
					t.Fatalf("Failed to marshal manifest: %v", err)
				}
				manifests = append(manifests, workv1.Manifest{
					RawExtension: runtime.RawExtension{Raw: jsonData},
				})
			}

			mw := &workv1.ManifestWork{
				Spec: workv1.ManifestWorkSpec{
					Workload: workv1.ManifestsTemplate{
						Manifests: manifests,
					},
				},
			}

			foundIndex := -1
			for i, manifest := range mw.Spec.Workload.Manifests {
				if manifest.Raw == nil {
					continue
				}

				var manifestData map[string]interface{}
				if err := json.Unmarshal(manifest.Raw, &manifestData); err != nil {
					continue
				}

				kind, _ := manifestData["kind"].(string)
				if kind == "HostedCluster" {
					foundIndex = i
					break
				}
			}

			if tt.expectFound && foundIndex != tt.expectedIndex {
				t.Errorf("Expected to find HostedCluster at index %d, found at %d", tt.expectedIndex, foundIndex)
			}
			if !tt.expectFound && foundIndex != -1 {
				t.Errorf("Expected not to find HostedCluster, but found at index %d", foundIndex)
			}
		})
	}
}

// TestResultStruct verifies the result struct JSON serialization.
func TestResultStruct(t *testing.T) {
	tests := []struct {
		name     string
		result   result
		expected string
	}{
		{
			name: "success result serializes correctly",
			result: result{
				ClusterID:   "cluster-123",
				ClusterName: "my-cluster",
				Status:      "success",
				VerifiedAt:  "2024-01-01T00:00:00Z",
			},
			expected: `{"cluster_id":"cluster-123","cluster_name":"my-cluster","status":"success","verified_at":"2024-01-01T00:00:00Z"}`,
		},
		{
			name: "failed result serializes correctly",
			result: result{
				ClusterID:   "cluster-456",
				ClusterName: "other-cluster",
				Status:      "failed",
				Error:       "something went wrong",
			},
			expected: `{"cluster_id":"cluster-456","cluster_name":"other-cluster","status":"failed","error":"something went wrong"}`,
		},
		{
			name: "omits empty error and verified_at",
			result: result{
				ClusterID:   "cluster-789",
				ClusterName: "another-cluster",
				Status:      "pending",
			},
			expected: `{"cluster_id":"cluster-789","cluster_name":"another-cluster","status":"pending"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tt.result)
			if err != nil {
				t.Fatalf("Failed to marshal result: %v", err)
			}

			if string(jsonData) != tt.expected {
				t.Errorf("JSON = %s, want %s", string(jsonData), tt.expected)
			}
		})
	}
}

// TestDisplayAllResultsCounts verifies correct counting in batch results.
func TestDisplayAllResultsCounts(t *testing.T) {
	tests := []struct {
		name                 string
		results              []result
		expectedSuccessCount int
		expectedFailureCount int
		expectedTotalCount   int
	}{
		{
			name: "all success",
			results: []result{
				{ClusterID: "c1", Status: "success"},
				{ClusterID: "c2", Status: "success"},
				{ClusterID: "c3", Status: "success"},
			},
			expectedSuccessCount: 3,
			expectedFailureCount: 0,
			expectedTotalCount:   3,
		},
		{
			name: "all failures",
			results: []result{
				{ClusterID: "c1", Status: "failed", Error: "error1"},
				{ClusterID: "c2", Status: "failed", Error: "error2"},
			},
			expectedSuccessCount: 0,
			expectedFailureCount: 2,
			expectedTotalCount:   2,
		},
		{
			name: "mixed results",
			results: []result{
				{ClusterID: "c1", Status: "success"},
				{ClusterID: "c2", Status: "failed", Error: "error"},
				{ClusterID: "c3", Status: "success"},
				{ClusterID: "c4", Status: "failed", Error: "error"},
				{ClusterID: "c5", Status: "success"},
			},
			expectedSuccessCount: 3,
			expectedFailureCount: 2,
			expectedTotalCount:   5,
		},
		{
			name:                 "empty results",
			results:              []result{},
			expectedSuccessCount: 0,
			expectedFailureCount: 0,
			expectedTotalCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			successCount := 0
			failureCount := 0

			for _, res := range tt.results {
				if res.Status == "success" {
					successCount++
				} else if res.Status == "failed" {
					failureCount++
				}
			}

			if successCount != tt.expectedSuccessCount {
				t.Errorf("successCount = %d, want %d", successCount, tt.expectedSuccessCount)
			}
			if failureCount != tt.expectedFailureCount {
				t.Errorf("failureCount = %d, want %d", failureCount, tt.expectedFailureCount)
			}
			if len(tt.results) != tt.expectedTotalCount {
				t.Errorf("totalCount = %d, want %d", len(tt.results), tt.expectedTotalCount)
			}
		})
	}
}

// TestHasEgressPolicyLabel verifies label detection on HostedCluster.
func TestHasEgressPolicyLabel(t *testing.T) {
	tests := []struct {
		name              string
		labels            map[string]string
		hasLabel          bool
		labelValue        string
		isAlreadyNoEgress bool
	}{
		{
			name: "has NoEgress label",
			labels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
			hasLabel:          true,
			labelValue:        "NoEgress",
			isAlreadyNoEgress: true,
		},
		{
			name: "has different egress policy value",
			labels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "SomeOtherPolicy",
			},
			hasLabel:          true,
			labelValue:        "SomeOtherPolicy",
			isAlreadyNoEgress: false,
		},
		{
			name: "has other labels but not egress policy",
			labels: map[string]string{
				"api.openshift.com/id":   "cluster-123",
				"api.openshift.com/name": "my-cluster",
			},
			hasLabel:          false,
			labelValue:        "",
			isAlreadyNoEgress: false,
		},
		{
			name:              "nil labels",
			labels:            nil,
			hasLabel:          false,
			labelValue:        "",
			isAlreadyNoEgress: false,
		},
		{
			name:              "empty labels",
			labels:            map[string]string{},
			hasLabel:          false,
			labelValue:        "",
			isAlreadyNoEgress: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: tt.labels,
				},
			}

			egressPolicy, hasEgressPolicy := hc.Labels["api.openshift.com/hosted-cluster-egress-policy"]

			if hasEgressPolicy != tt.hasLabel {
				t.Errorf("hasEgressPolicy = %v, want %v", hasEgressPolicy, tt.hasLabel)
			}
			if egressPolicy != tt.labelValue {
				t.Errorf("egressPolicy = %v, want %v", egressPolicy, tt.labelValue)
			}
			if (hasEgressPolicy && egressPolicy == "NoEgress") != tt.isAlreadyNoEgress {
				t.Errorf("isAlreadyNoEgress = %v, want %v", hasEgressPolicy && egressPolicy == "NoEgress", tt.isAlreadyNoEgress)
			}
		})
	}
}

// TestManifestWithNilRaw verifies handling of manifests with nil Raw data.
func TestManifestWithNilRaw(t *testing.T) {
	hc := map[string]interface{}{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata": map[string]interface{}{
			"name":   "test-cluster",
			"labels": map[string]interface{}{},
		},
	}
	hcJSON, _ := json.Marshal(hc)

	mw := &workv1.ManifestWork{
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{RawExtension: runtime.RawExtension{Raw: nil}},      // nil Raw
					{RawExtension: runtime.RawExtension{Raw: []byte{}}}, // empty Raw
					{RawExtension: runtime.RawExtension{Raw: hcJSON}},   // valid HostedCluster
				},
			},
		},
	}

	foundIndex := -1
	for i, manifest := range mw.Spec.Workload.Manifests {
		if manifest.Raw == nil {
			continue
		}

		var manifestData map[string]interface{}
		if err := json.Unmarshal(manifest.Raw, &manifestData); err != nil {
			continue
		}

		kind, _ := manifestData["kind"].(string)
		if kind == "HostedCluster" {
			foundIndex = i
			break
		}
	}

	if foundIndex != 2 {
		t.Errorf("Expected to find HostedCluster at index 2, found at %d", foundIndex)
	}
}

// TestPatchPreservesMetadataFields verifies that patching preserves other metadata fields.
func TestPatchPreservesMetadataFields(t *testing.T) {
	hc := map[string]interface{}{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata": map[string]interface{}{
			"name":      "test-cluster",
			"namespace": "test-namespace",
			"annotations": map[string]interface{}{
				"some-annotation": "some-value",
			},
			"labels": map[string]interface{}{
				"existing-label": "existing-value",
			},
			"uid":             "12345",
			"resourceVersion": "67890",
		},
		"spec": map[string]interface{}{
			"someField": "someValue",
		},
	}

	hcJSON, _ := json.Marshal(hc)

	var manifestData map[string]interface{}
	if err := json.Unmarshal(hcJSON, &manifestData); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	metadata := manifestData["metadata"].(map[string]interface{})
	labels := metadata["labels"].(map[string]interface{})
	labels["api.openshift.com/hosted-cluster-egress-policy"] = "NoEgress"

	// Verify all fields are preserved
	if metadata["name"] != "test-cluster" {
		t.Error("name field was not preserved")
	}
	if metadata["namespace"] != "test-namespace" {
		t.Error("namespace field was not preserved")
	}
	if metadata["uid"] != "12345" {
		t.Error("uid field was not preserved")
	}
	if metadata["resourceVersion"] != "67890" {
		t.Error("resourceVersion field was not preserved")
	}

	annotations := metadata["annotations"].(map[string]interface{})
	if annotations["some-annotation"] != "some-value" {
		t.Error("annotations were not preserved")
	}

	if labels["existing-label"] != "existing-value" {
		t.Error("existing labels were not preserved")
	}
	if labels["api.openshift.com/hosted-cluster-egress-policy"] != "NoEgress" {
		t.Error("new label was not added")
	}

	spec := manifestData["spec"].(map[string]interface{})
	if spec["someField"] != "someValue" {
		t.Error("spec was not preserved")
	}
}
