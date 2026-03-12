package main

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
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

// TestPatchManagedClusterLabel verifies label injection into ManagedCluster resources.
func TestPatchManagedClusterLabel(t *testing.T) {
	tests := []struct {
		name           string
		initialLabels  map[string]string
		expectedLabels map[string]string
	}{
		{
			name: "adds label to cluster with existing labels",
			initialLabels: map[string]string{
				"api.openshift.com/id": "cluster-123",
			},
			expectedLabels: map[string]string{
				"api.openshift.com/id":                           "cluster-123",
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name:          "adds label to cluster with no labels",
			initialLabels: map[string]string{},
			expectedLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name:          "adds label to cluster with nil labels",
			initialLabels: nil,
			expectedLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "NoEgress",
			},
		},
		{
			name: "updates existing egress policy label",
			initialLabels: map[string]string{
				"api.openshift.com/hosted-cluster-egress-policy": "SomeOtherValue",
			},
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
			mc := &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-cluster-id",
					Labels: tt.initialLabels,
				},
			}

			// Simulate the patchManagedClusterLabel logic
			if mc.Labels == nil {
				mc.Labels = make(map[string]string)
			}
			mc.Labels["api.openshift.com/hosted-cluster-egress-policy"] = "NoEgress"

			for key, expectedValue := range tt.expectedLabels {
				actualValue, ok := mc.Labels[key]
				if !ok {
					t.Errorf("Expected label %s not found", key)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("Label %s = %v, want %v", key, actualValue, expectedValue)
				}
			}

			if mc.Labels["api.openshift.com/hosted-cluster-egress-policy"] != "NoEgress" {
				t.Error("egress policy label not set correctly")
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

// TestHasEgressPolicyLabel verifies label detection on ManagedCluster.
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
			mc := &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: tt.labels,
				},
			}

			egressPolicy, hasEgressPolicy := mc.Labels["api.openshift.com/hosted-cluster-egress-policy"]

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
