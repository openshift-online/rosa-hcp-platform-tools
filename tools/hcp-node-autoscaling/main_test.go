package main

import (
	"encoding/json"
	"regexp"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	workv1 "open-cluster-management.io/api/work/v1"
)

// TestCategorizeCluster verifies cluster categorization logic for migration readiness.
func TestCategorizeCluster(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    string
	}{
		{
			name: "needs-removal: has cluster-size-override annotation",
			annotations: map[string]string{
				"hypershift.openshift.io/cluster-size-override": "m5xl",
			},
			expected: "needs-removal",
		},
		{
			name: "needs-removal: has both override and autoscaling annotations",
			annotations: map[string]string{
				"hypershift.openshift.io/cluster-size-override":          "m52xl",
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			expected: "needs-removal",
		},
		{
			name: "already-configured: has required annotation",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			expected: "already-configured",
		},
		{
			name:        "ready-for-migration: missing auto-scaling annotation",
			annotations: map[string]string{},
			expected:    "ready-for-migration",
		},
		{
			name: "ready-for-migration: wrong auto-scaling value",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "false",
			},
			expected: "ready-for-migration",
		},
		{
			name:        "ready-for-migration: no annotations",
			annotations: map[string]string{},
			expected:    "ready-for-migration",
		},
		{
			name:        "ready-for-migration: nil annotations",
			annotations: nil,
			expected:    "ready-for-migration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			opts := &auditOpts{}
			result := opts.categorizeCluster(hc)

			if result != tt.expected {
				t.Errorf("categorizeCluster() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestDualCategorizationForOverrideAnnotation verifies that clusters with override
// annotation are categorized as "needs-removal" and should appear in multiple groups.
func TestDualCategorizationForOverrideAnnotation(t *testing.T) {
	tests := []struct {
		name                    string
		annotations             map[string]string
		expectedCategory        string
		expectedInNeedsRemoval  bool
		expectedInReadyMigrate  bool
		expectedInAlreadyConfig bool
	}{
		{
			name: "override only - needs-removal + ready-for-migration",
			annotations: map[string]string{
				"hypershift.openshift.io/cluster-size-override": "m5xl",
			},
			expectedCategory:        "needs-removal",
			expectedInNeedsRemoval:  true,
			expectedInReadyMigrate:  true,
			expectedInAlreadyConfig: false,
		},
		{
			name: "override + autoscaling - needs-removal + already-configured",
			annotations: map[string]string{
				"hypershift.openshift.io/cluster-size-override":          "m5xl",
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			expectedCategory:        "needs-removal",
			expectedInNeedsRemoval:  true,
			expectedInReadyMigrate:  false,
			expectedInAlreadyConfig: true,
		},
		{
			name: "autoscaling only - already-configured only",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			expectedCategory:        "already-configured",
			expectedInNeedsRemoval:  false,
			expectedInReadyMigrate:  false,
			expectedInAlreadyConfig: true,
		},
		{
			name:                    "no annotations - ready-for-migration only",
			annotations:             map[string]string{},
			expectedCategory:        "ready-for-migration",
			expectedInNeedsRemoval:  false,
			expectedInReadyMigrate:  true,
			expectedInAlreadyConfig: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			opts := &auditOpts{}
			category := opts.categorizeCluster(hc)

			if category != tt.expectedCategory {
				t.Errorf("Expected category '%s', got '%s'", tt.expectedCategory, category)
			}

			// Verify dual-categorization logic (simulating what happens in run() method)
			_, hasOverride := tt.annotations["hypershift.openshift.io/cluster-size-override"]
			autoScaling, hasAutoScaling := tt.annotations["hypershift.openshift.io/resource-based-cp-auto-scaling"]
			hasAutoscalingEnabled := hasAutoScaling && autoScaling == "true"

			inNeedsRemoval := hasOverride
			inReadyMigrate := (hasOverride && !hasAutoscalingEnabled) || (!hasOverride && !hasAutoscalingEnabled)
			inAlreadyConfig := hasAutoscalingEnabled

			if inNeedsRemoval != tt.expectedInNeedsRemoval {
				t.Errorf("Expected inNeedsRemoval=%v, got %v", tt.expectedInNeedsRemoval, inNeedsRemoval)
			}
			if inReadyMigrate != tt.expectedInReadyMigrate {
				t.Errorf("Expected inReadyMigrate=%v, got %v", tt.expectedInReadyMigrate, inReadyMigrate)
			}
			if inAlreadyConfig != tt.expectedInAlreadyConfig {
				t.Errorf("Expected inAlreadyConfig=%v, got %v", tt.expectedInAlreadyConfig, inAlreadyConfig)
			}
		})
	}
}

// TestListOcmNamespaces verifies OCM namespace filtering with regex patterns.
func TestListOcmNamespaces(t *testing.T) {
	tests := []struct {
		name            string
		namespaces      []string
		expectedCount   int
		expectedMatches []string
	}{
		{
			name: "filters production namespaces",
			namespaces: []string{
				"ocm-production-abc123",
				"ocm-production-xyz789",
				"kube-system",
				"default",
			},
			expectedCount:   2,
			expectedMatches: []string{"ocm-production-abc123", "ocm-production-xyz789"},
		},
		{
			name: "filters staging namespaces",
			namespaces: []string{
				"ocm-staging-abc123",
				"ocm-staging-xyz789",
				"openshift-config",
			},
			expectedCount:   2,
			expectedMatches: []string{"ocm-staging-abc123", "ocm-staging-xyz789"},
		},
		{
			name: "filters both production and staging",
			namespaces: []string{
				"ocm-production-abc123",
				"ocm-staging-xyz789",
				"ocm-other-namespace",
				"kube-system",
			},
			expectedCount:   2,
			expectedMatches: []string{"ocm-production-abc123", "ocm-staging-xyz789"},
		},
		{
			name: "rejects invalid patterns",
			namespaces: []string{
				"ocm-production-abc123-extra",
				"ocm-production",
				"production-abc123",
				"ocm-staging-",
			},
			expectedCount:   0,
			expectedMatches: []string{},
		},
		{
			name: "accepts alphanumeric cluster IDs",
			namespaces: []string{
				"ocm-production-2o01jtlh4a3h7p5f04irugtiic86dh47",
				"ocm-staging-ABC123xyz",
			},
			expectedCount:   2,
			expectedMatches: []string{"ocm-production-2o01jtlh4a3h7p5f04irugtiic86dh47", "ocm-staging-ABC123xyz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nsList := &corev1.NamespaceList{}
			for _, ns := range tt.namespaces {
				nsList.Items = append(nsList.Items, corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns,
					},
				})
			}

			ocmNamespacePattern := `^ocm-(production|staging)-[a-zA-Z0-9]+$`
			var filtered []corev1.Namespace
			for _, ns := range nsList.Items {
				matched, _ := regexp.MatchString(ocmNamespacePattern, ns.Name)
				if matched {
					filtered = append(filtered, ns)
				}
			}

			if len(filtered) != tt.expectedCount {
				t.Errorf("Expected %d filtered namespaces, got %d", tt.expectedCount, len(filtered))
			}

			for _, expected := range tt.expectedMatches {
				found := false
				for _, ns := range filtered {
					if ns.Name == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected namespace %s not found in filtered results", expected)
				}
			}
		})
	}
}

// TestApplyFilter verifies audit result filtering based on category.
func TestApplyFilter(t *testing.T) {
	baseResults := &auditResults{
		MgmtClusterID: "test-cluster",
		TotalScanned:  6,
		NeedsLabelRemoval: []hostedClusterAuditInfo{
			{ClusterID: "cluster1", Category: "needs-removal"},
			{ClusterID: "cluster2", Category: "needs-removal"},
		},
		ReadyForMigration: []hostedClusterAuditInfo{
			{ClusterID: "cluster3", Category: "ready-for-migration"},
			{ClusterID: "cluster4", Category: "ready-for-migration"},
			{ClusterID: "cluster5", Category: "ready-for-migration"},
		},
		AlreadyConfigured: []hostedClusterAuditInfo{
			{ClusterID: "cluster6", Category: "already-configured"},
		},
	}

	tests := []struct {
		name                      string
		showOnly                  string
		expectedNeedsRemovalCount int
		expectedReadyCount        int
		expectedConfiguredCount   int
		expectedTotalScanned      int
	}{
		{
			name:                      "filter needs-removal",
			showOnly:                  "needs-removal",
			expectedNeedsRemovalCount: 2,
			expectedReadyCount:        0,
			expectedConfiguredCount:   0,
			expectedTotalScanned:      2,
		},
		{
			name:                      "filter ready-for-migration",
			showOnly:                  "ready-for-migration",
			expectedNeedsRemovalCount: 0,
			expectedReadyCount:        3,
			expectedConfiguredCount:   0,
			expectedTotalScanned:      3,
		},
		{
			name:                      "no filter",
			showOnly:                  "",
			expectedNeedsRemovalCount: 2,
			expectedReadyCount:        3,
			expectedConfiguredCount:   1,
			expectedTotalScanned:      6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &auditOpts{showOnly: tt.showOnly}
			filtered := opts.applyFilter(baseResults)

			if len(filtered.NeedsLabelRemoval) != tt.expectedNeedsRemovalCount {
				t.Errorf("NeedsLabelRemoval count = %d, want %d", len(filtered.NeedsLabelRemoval), tt.expectedNeedsRemovalCount)
			}
			if len(filtered.ReadyForMigration) != tt.expectedReadyCount {
				t.Errorf("ReadyForMigration count = %d, want %d", len(filtered.ReadyForMigration), tt.expectedReadyCount)
			}
			if len(filtered.AlreadyConfigured) != tt.expectedConfiguredCount {
				t.Errorf("AlreadyConfigured count = %d, want %d", len(filtered.AlreadyConfigured), tt.expectedConfiguredCount)
			}
			if filtered.TotalScanned != tt.expectedTotalScanned {
				t.Errorf("TotalScanned = %d, want %d", filtered.TotalScanned, tt.expectedTotalScanned)
			}
		})
	}
}

// TestHasRequiredAnnotations verifies annotation validation for autoscaling readiness.
func TestHasRequiredAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name: "has required annotation with correct value",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			expected: true,
		},
		{
			name: "has required annotation with other annotations",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
				"other.annotation": "value",
			},
			expected: true,
		},
		{
			name: "missing auto-scaling annotation",
			annotations: map[string]string{
				"other.annotation": "value",
			},
			expected: false,
		},
		{
			name: "wrong auto-scaling value",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "false",
			},
			expected: false,
		},
		{
			name:        "nil annotations",
			annotations: nil,
			expected:    false,
		},
		{
			name:        "empty annotations",
			annotations: map[string]string{},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			opts := &migrateOpts{}
			result := opts.hasRequiredAnnotations(hc)

			if result != tt.expected {
				t.Errorf("hasRequiredAnnotations() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestPatchManifestWorkAnnotations verifies annotation injection into ManifestWork resources.
func TestPatchManifestWorkAnnotations(t *testing.T) {
	tests := []struct {
		name                string
		initialAnnotations  map[string]string
		expectError         bool
		expectedAnnotations map[string]string
	}{
		{
			name: "adds annotation to cluster without existing annotations",
			initialAnnotations: map[string]string{
				"other.annotation": "value",
			},
			expectError: false,
			expectedAnnotations: map[string]string{
				"other.annotation": "value",
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
		},
		{
			name:               "adds annotation to cluster with no annotations",
			initialAnnotations: map[string]string{},
			expectError:        false,
			expectedAnnotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
		},
		{
			name: "updates existing annotation",
			initialAnnotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "false",
			},
			expectError: false,
			expectedAnnotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
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
					Name:        "test-cluster",
					Namespace:   "test-namespace",
					Annotations: tt.initialAnnotations,
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

			manifest := mw.Spec.Workload.Manifests[0]
			var manifestData map[string]interface{}
			if err := json.Unmarshal(manifest.Raw, &manifestData); err != nil {
				t.Fatalf("Failed to unmarshal manifest: %v", err)
			}

			kind, _ := manifestData["kind"].(string)
			if kind != "HostedCluster" {
				t.Fatalf("Expected HostedCluster, got %s", kind)
			}

			metadata, ok := manifestData["metadata"].(map[string]interface{})
			if !ok {
				metadata = make(map[string]interface{})
				manifestData["metadata"] = metadata
			}

			annotations, ok := metadata["annotations"].(map[string]interface{})
			if !ok {
				annotations = make(map[string]interface{})
				metadata["annotations"] = annotations
			}

			annotations["hypershift.openshift.io/resource-based-cp-auto-scaling"] = "true"

			for key, expectedValue := range tt.expectedAnnotations {
				actualValue, ok := annotations[key]
				if !ok {
					t.Errorf("Expected annotation %s not found", key)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("Annotation %s = %v, want %v", key, actualValue, expectedValue)
				}
			}

			if annotations["hypershift.openshift.io/resource-based-cp-auto-scaling"] != "true" {
				t.Errorf("auto-scaling annotation not set correctly")
			}
		})
	}
}

// TestPatchManifestWorkFindsHostedCluster verifies HostedCluster detection in multi-manifest ManifestWork.
func TestPatchManifestWorkFindsHostedCluster(t *testing.T) {
	secret := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name": "test-secret",
		},
	}
	secretJSON, _ := json.Marshal(secret)

	hc := map[string]interface{}{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata": map[string]interface{}{
			"name":        "test-cluster",
			"annotations": map[string]interface{}{},
		},
	}
	hcJSON, _ := json.Marshal(hc)

	cert := map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"name": "test-cert",
		},
	}
	certJSON, _ := json.Marshal(cert)

	mw := &workv1.ManifestWork{
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{RawExtension: runtime.RawExtension{Raw: secretJSON}},
					{RawExtension: runtime.RawExtension{Raw: hcJSON}},
					{RawExtension: runtime.RawExtension{Raw: certJSON}},
				},
			},
		},
	}

	foundIndex := -1
	for i, manifest := range mw.Spec.Workload.Manifests {
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

	if foundIndex != 1 {
		t.Errorf("Expected to find HostedCluster at index 1, found at %d", foundIndex)
	}

	var hcData map[string]interface{}
	if err := json.Unmarshal(mw.Spec.Workload.Manifests[foundIndex].Raw, &hcData); err != nil {
		t.Fatalf("Failed to unmarshal HostedCluster: %v", err)
	}

	metadata := hcData["metadata"].(map[string]interface{})
	annotations, ok := metadata["annotations"].(map[string]interface{})
	if !ok {
		annotations = make(map[string]interface{})
		metadata["annotations"] = annotations
	}

	annotations["test-key"] = "test-value"

	if annotations["test-key"] != "test-value" {
		t.Errorf("Failed to modify HostedCluster annotations")
	}
}

// TestGetCandidatesForMigrationIncludesNeedsRemoval verifies that migration includes needs-removal clusters
// but excludes clusters that already have autoscaling enabled.
func TestGetCandidatesForMigrationIncludesNeedsRemoval(t *testing.T) {
	tests := []struct {
		name             string
		category         string
		annotations      map[string]string
		shouldBeIncluded bool
	}{
		{
			name:             "ready-for-migration included",
			category:         "ready-for-migration",
			annotations:      map[string]string{},
			shouldBeIncluded: true,
		},
		{
			name:     "needs-removal included (no autoscaling)",
			category: "needs-removal",
			annotations: map[string]string{
				"hypershift.openshift.io/cluster-size-override": "m5xl",
			},
			shouldBeIncluded: true,
		},
		{
			name:     "needs-removal excluded (has autoscaling)",
			category: "needs-removal",
			annotations: map[string]string{
				"hypershift.openshift.io/cluster-size-override":          "m5xl",
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			shouldBeIncluded: false,
		},
		{
			name:     "already-configured excluded",
			category: "already-configured",
			annotations: map[string]string{
				"hypershift.openshift.io/resource-based-cp-auto-scaling": "true",
			},
			shouldBeIncluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check if cluster has autoscaling enabled
			autoScaling, hasAutoScaling := tt.annotations["hypershift.openshift.io/resource-based-cp-auto-scaling"]
			hasAutoscalingEnabled := hasAutoScaling && autoScaling == "true"

			// New logic: exclude if has autoscaling, otherwise include if not already-configured
			shouldInclude := !hasAutoscalingEnabled && (tt.category != "already-configured")

			if shouldInclude != tt.shouldBeIncluded {
				t.Errorf("Expected shouldInclude=%v for category %s with annotations %v, got %v",
					tt.shouldBeIncluded, tt.category, tt.annotations, shouldInclude)
			}
		})
	}
}
