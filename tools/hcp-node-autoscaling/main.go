package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	sdk "github.com/openshift-online/ocm-sdk-go"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/osdctl/pkg/k8s"
	"github.com/openshift/osdctl/pkg/printer"
	"github.com/openshift/osdctl/pkg/utils"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	annotationResourceBasedAutoscaling = "hypershift.openshift.io/resource-based-cp-auto-scaling"
	annotationClusterSizeOverride      = "hypershift.openshift.io/cluster-size-override"
	annotationRecommendedClusterSize   = "hypershift.openshift.io/recommended-cluster-size"

	labelHostedClusterSize = "hypershift.openshift.io/hosted-cluster-size"
	labelClusterID         = "api.openshift.com/id"
)

type auditOpts struct {
	mgmtClusterID string
	fleet         bool
	output        string
	showOnly      string
	noHeaders     bool
	concurrency   int

	mgmtClient     client.Client
	k8sClientMutex *sync.Mutex
}

type hostedClusterAuditInfo struct {
	ClusterID   string            `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string            `json:"cluster_name" yaml:"cluster_name"`
	Namespace   string            `json:"namespace" yaml:"namespace"`
	CurrentSize string            `json:"current_size" yaml:"current_size"`
	Category    string            `json:"category" yaml:"category"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type auditResults struct {
	MgmtClusterID     string                   `json:"mgmt_cluster_id" yaml:"mgmt_cluster_id"`
	TotalScanned      int                      `json:"total_scanned" yaml:"total_scanned"`
	NeedsLabelRemoval []hostedClusterAuditInfo `json:"needs_label_removal" yaml:"needs_label_removal"`
	ReadyForMigration []hostedClusterAuditInfo `json:"ready_for_migration" yaml:"ready_for_migration"`
	AlreadyConfigured []hostedClusterAuditInfo `json:"already_configured" yaml:"already_configured"`
	Errors            []auditError             `json:"errors,omitempty" yaml:"errors,omitempty"`
}

type auditError struct {
	Namespace string `json:"namespace" yaml:"namespace"`
	Error     string `json:"error" yaml:"error"`
}

type fleetAuditResults struct {
	Timestamp               time.Time          `json:"timestamp" yaml:"timestamp"`
	TotalManagementClusters int                `json:"total_mgmt_clusters" yaml:"total_mgmt_clusters"`
	TotalHostedClusters     int                `json:"total_hosted_clusters" yaml:"total_hosted_clusters"`
	Clusters                []fleetClusterInfo `json:"clusters" yaml:"clusters"`
	Errors                  []fleetAuditError  `json:"errors,omitempty" yaml:"errors,omitempty"`
}

type fleetClusterInfo struct {
	ManagementClusterID   string `json:"mgmt_cluster_id" yaml:"mgmt_cluster_id" csv:"mgmt_cluster_id"` // Actually stores management cluster name
	ClusterID             string `json:"cluster_id" yaml:"cluster_id" csv:"cluster_id"`
	ClusterName           string `json:"cluster_name" yaml:"cluster_name" csv:"cluster_name"`
	AutoscalingEnabled    bool   `json:"autoscaling_enabled" yaml:"autoscaling_enabled" csv:"autoscaling_enabled"`
	HasOverrideAnnotation bool   `json:"has_override" yaml:"has_override" csv:"has_override"`
	CurrentSize           string `json:"current_size" yaml:"current_size" csv:"current_size"`
	RecommendedSize       string `json:"recommended_size" yaml:"recommended_size" csv:"recommended_size"`
}

type fleetAuditError struct {
	ManagementClusterID string `json:"mgmt_cluster_id" yaml:"mgmt_cluster_id"`
	Namespace           string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Error               string `json:"error" yaml:"error"`
}

type migrateOpts struct {
	serviceClusterID string
	mgmtClusterID    string
	dryRun           bool
	skipConfirmation bool
	reason           string
	serviceClient    client.Client
	mgmtClient       client.Client
	ocmConn          *sdk.Connection
	mgmtClusterName  string
}

type migrationResult struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	VerifiedAt  string `json:"verified_at,omitempty"`
}

type rollbackOpts struct {
	hostedClusterID  string
	dryRun           bool
	skipConfirmation bool
	reason           string
	serviceClient    client.Client
	mgmtClient       client.Client
	ocmConn          *sdk.Connection
	serviceClusterID string
	mgmtClusterID    string
	mgmtClusterName  string
}

func main() {
	// k8sClientMutex protects concurrent k8s client creation which uses non-thread-safe viper
	k8sClientMutex := &sync.Mutex{}

	rootCmd := &cobra.Command{
		Use:   "hcp-node-autoscaling",
		Short: "HCP node autoscaling audit and migration tool",
		Long: `A tool for auditing and migrating hosted clusters on ROSA HCP management clusters
for node autoscaling readiness.

Use the audit subcommand to analyze clusters and the migrate subcommand to perform
the actual migration.`,
	}

	rootCmd.AddCommand(newAuditCmd(k8sClientMutex))
	rootCmd.AddCommand(newMigrateCmd())
	rootCmd.AddCommand(newRollbackCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// newAuditCmd creates the audit subcommand for analyzing hosted clusters.
func newAuditCmd(k8sClientMutex *sync.Mutex) *cobra.Command {
	opts := &auditOpts{
		k8sClientMutex: k8sClientMutex,
	}
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Audit hosted clusters on management cluster(s) for autoscaling readiness",
		Long: `Audit hosted clusters to determine their autoscaling migration readiness.

Can audit a single management cluster or the entire fleet.

Single cluster mode:
  Requires --mgmt-cluster-id flag
  Outputs detailed categorization (Group A, Group B, Already Configured)

Fleet mode:
  Use --fleet flag to audit all management clusters
  Outputs flat table with autoscaling status across entire fleet`,
		Example: `
  # Audit a single management cluster
  hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123

  # Audit entire fleet
  hcp-node-autoscaling audit --fleet

  # Fleet audit with CSV output
  hcp-node-autoscaling audit --fleet --output csv

  # Fleet audit - show only clusters ready for migration
  hcp-node-autoscaling audit --fleet --show-only ready-for-migration

  # Fleet audit - show only clusters that need annotation removal
  hcp-node-autoscaling audit --fleet --show-only needs-removal

  # Fleet audit - show only clusters safe to remove override (autoscaling enabled, has override, sizes match)
  hcp-node-autoscaling audit --fleet --show-only safe-to-remove-override

  # Single cluster - show only clusters that need annotation removal
  hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --show-only needs-removal

  # Single cluster - export to JSON for scripting
  hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --output json
`,
		Args:              cobra.NoArgs,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(context.Background())
		},
	}

	cmd.Flags().StringVar(&opts.mgmtClusterID, "mgmt-cluster-id", "",
		"Management cluster ID to audit")
	cmd.Flags().BoolVar(&opts.fleet, "fleet", false,
		"Audit all management clusters in the fleet")
	cmd.Flags().StringVar(&opts.output, "output", "text",
		"Output format: text, json, yaml, csv")
	cmd.Flags().StringVar(&opts.showOnly, "show-only", "",
		"Filter output: needs-removal, ready-for-migration, safe-to-remove-override")
	cmd.Flags().BoolVar(&opts.noHeaders, "no-headers", false,
		"Skip table headers in output")
	cmd.Flags().IntVar(&opts.concurrency, "concurrency", 10,
		"Number of management clusters to audit concurrently (fleet mode only)")

	cmd.MarkFlagsMutuallyExclusive("mgmt-cluster-id", "fleet")
	cmd.MarkFlagsOneRequired("mgmt-cluster-id", "fleet")

	return cmd
}

// newMigrateCmd creates the migrate subcommand for migrating clusters to autoscaling.
func newMigrateCmd() *cobra.Command {
	opts := &migrateOpts{}
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Enable autoscaling for hosted clusters on a service cluster",
		Long: `Perform autoscaling migration for hosted clusters.

This command will:
1. Audit the management cluster to find clusters for migration
2. Include clusters without autoscaling (even if they have cluster-size-override)
3. Display the list and ask for confirmation
4. Patch ManifestWork resources on the service cluster
5. Verify the annotations are synced to the management cluster
6. Report results

Note: Clusters with cluster-size-override annotation will also be migrated.
The override annotation will remain - review and remove manually if needed.`,
		Example: `
  # Migrate clusters with confirmation
  hcp-node-autoscaling migrate \
    --service-cluster-id svc-123 \
    --mgmt-cluster-id mgmt-456 \
    --reason "Enabling request serving node autoscaling"

  # Dry run to see what would be migrated
  hcp-node-autoscaling migrate \
    --service-cluster-id svc-123 \
    --mgmt-cluster-id mgmt-456 \
    --reason "Enabling request serving node autoscaling" \
    --dry-run

  # Skip confirmation prompt (use with caution)
  hcp-node-autoscaling migrate \
    --service-cluster-id svc-123 \
    --mgmt-cluster-id mgmt-456 \
    --reason "Enabling request serving node autoscaling" \
    --skip-confirmation`,
		Args:              cobra.NoArgs,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(context.Background())
		},
	}

	cmd.Flags().StringVar(&opts.serviceClusterID, "service-cluster-id", "",
		"The service cluster ID where ManifestWork resources exist")
	cmd.Flags().StringVar(&opts.mgmtClusterID, "mgmt-cluster-id", "",
		"The management cluster ID to migrate")
	cmd.Flags().StringVar(&opts.reason, "reason", "",
		"Reason for elevation (e.g., 'Enabling request serving node autoscaling')")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false,
		"Preview changes without applying them")
	cmd.Flags().BoolVar(&opts.skipConfirmation, "skip-confirmation", false,
		"Skip confirmation prompt (use with caution)")

	_ = cmd.MarkFlagRequired("service-cluster-id")
	_ = cmd.MarkFlagRequired("mgmt-cluster-id")
	_ = cmd.MarkFlagRequired("reason")

	return cmd
}

// newRollbackCmd creates the rollback subcommand for removing autoscaling from hosted clusters.
func newRollbackCmd() *cobra.Command {
	opts := &rollbackOpts{}
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback autoscaling for a hosted cluster",
		Long: `Remove autoscaling annotations from a hosted cluster by patching its ManifestWork resource.

This command will:
1. Look up the management and service clusters from OCM using the hosted cluster ID
2. Connect to the management cluster to find the hosted cluster
3. Connect to the service cluster with elevated permissions
4. Remove the autoscaling annotation from the ManifestWork
5. Verify the annotations are synced back to the management cluster`,
		Example: `
  # Rollback autoscaling for a hosted cluster
  hcp-node-autoscaling rollback \
    --hosted-cluster-id 1a2b3c4d5e6f7g8h9i0j \
    --reason "Rolling back hosted cluster autoscaling migration"

  # Dry run to see what would be changed
  hcp-node-autoscaling rollback \
    --hosted-cluster-id cluster-123 \
    --reason "Rolling back hosted cluster autoscaling migration" \
    --dry-run

  # Skip confirmation prompt (use with caution)
  hcp-node-autoscaling rollback \
    --hosted-cluster-id cluster-123 \
    --reason "Rolling back hosted cluster autoscaling migration" \
    --skip-confirmation`,
		Args:              cobra.NoArgs,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(context.Background())
		},
	}

	cmd.Flags().StringVar(&opts.hostedClusterID, "hosted-cluster-id", "",
		"The hosted cluster ID/name/external-id to rollback")
	cmd.Flags().StringVar(&opts.reason, "reason", "",
		"Reason for elevation (e.g., 'Rolling back hosted cluster autoscaling migration')")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false,
		"Preview changes without applying them")
	cmd.Flags().BoolVar(&opts.skipConfirmation, "skip-confirmation", false,
		"Skip confirmation prompt (use with caution)")

	_ = cmd.MarkFlagRequired("hosted-cluster-id")
	_ = cmd.MarkFlagRequired("reason")

	return cmd
}

// run executes the audit command to analyze hosted clusters for autoscaling readiness.
func (a *auditOpts) run(ctx context.Context) error {
	validOutputs := map[string]bool{"text": true, "json": true, "yaml": true, "csv": true}
	if !validOutputs[a.output] {
		return fmt.Errorf("invalid output format '%s'. Valid options: text, json, yaml, csv", a.output)
	}

	if a.showOnly != "" {
		validFilters := map[string]bool{"needs-removal": true, "ready-for-migration": true, "safe-to-remove-override": true}
		if !validFilters[a.showOnly] {
			return fmt.Errorf("invalid show-only filter '%s'. Valid options: needs-removal, ready-for-migration, safe-to-remove-override", a.showOnly)
		}
	}

	connection, err := utils.CreateConnection()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %v", err)
	}
	defer connection.Close()

	if a.fleet {
		return a.runFleetAudit(ctx, connection)
	}
	return a.runSingleClusterAudit(ctx, connection)
}

// runFleetAudit audits all management clusters in the fleet.
func (a *auditOpts) runFleetAudit(ctx context.Context, conn *sdk.Connection) error {
	fmt.Println("Fleet-wide audit mode: Fetching management clusters...")

	mgmtClusterIDs, err := getManagementClustersFromFleet(conn)
	if err != nil {
		return fmt.Errorf("failed to get management clusters: %v", err)
	}

	fmt.Printf("Found %d management clusters in fleet\n", len(mgmtClusterIDs))
	fmt.Printf("Auditing clusters with concurrency limit of %d...\n\n", a.concurrency)

	fleetResults := &fleetAuditResults{
		Timestamp:               time.Now(),
		TotalManagementClusters: len(mgmtClusterIDs),
		Clusters:                []fleetClusterInfo{},
		Errors:                  []fleetAuditError{},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	type mcResult struct {
		mgmtClusterID string
		clusters      []fleetClusterInfo
		err           error
	}
	resultsChan := make(chan mcResult, len(mgmtClusterIDs))

	semaphore := make(chan struct{}, a.concurrency)

	for _, mgmtClusterID := range mgmtClusterIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			auditCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			mcClusters, err := a.auditSingleManagementCluster(auditCtx, conn, id)

			resultsChan <- mcResult{
				mgmtClusterID: id,
				clusters:      mcClusters,
				err:           err,
			}
		}(mgmtClusterID)
	}

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	completedCount := 0
	for result := range resultsChan {
		completedCount++
		fmt.Printf("Progress: %d/%d management clusters audited\n", completedCount, len(mgmtClusterIDs))

		if result.err != nil {
			fmt.Printf("  • Management Cluster %s: %v\n", result.mgmtClusterID, result.err)
			mu.Lock()
			fleetResults.Errors = append(fleetResults.Errors, fleetAuditError{
				ManagementClusterID: result.mgmtClusterID,
				Error:               result.err.Error(),
			})
			mu.Unlock()
			continue
		}

		mu.Lock()
		fleetResults.Clusters = append(fleetResults.Clusters, result.clusters...)
		mu.Unlock()
	}

	fmt.Println()
	fleetResults.TotalHostedClusters = len(fleetResults.Clusters)

	if a.showOnly != "" {
		fleetResults = a.applyFleetFilter(fleetResults)
	}

	return a.outputFleetResults(fleetResults)
}

// runSingleClusterAudit audits a single management cluster.
func (a *auditOpts) runSingleClusterAudit(ctx context.Context, conn *sdk.Connection) error {
	if err := utils.IsValidClusterKey(a.mgmtClusterID); err != nil {
		return err
	}

	cluster, err := utils.GetCluster(conn, a.mgmtClusterID)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %v", err)
	}

	isMC, err := utils.IsManagementCluster(cluster.ID())
	if err != nil {
		return fmt.Errorf("failed to verify if cluster is a management cluster: %v", err)
	}
	if !isMC {
		return fmt.Errorf("cluster %s is not a management cluster", cluster.ID())
	}

	a.mgmtClusterID = cluster.ID()

	fmt.Printf("Auditing management cluster: %s (%s)\n", cluster.Name(), cluster.ID())

	scheme := runtime.NewScheme()
	if err := hypershiftv1beta1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add hypershift scheme: %v", err)
	}

	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add core v1 scheme: %v", err)
	}

	a.k8sClientMutex.Lock()
	mgmtClient, err := k8s.New(a.mgmtClusterID, client.Options{Scheme: scheme})
	a.k8sClientMutex.Unlock()
	if err != nil {
		return fmt.Errorf("failed to create management cluster client: %v", err)
	}
	a.mgmtClient = mgmtClient

	namespaces, err := a.listOcmNamespacesForCluster(ctx, mgmtClient)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}

	fmt.Printf("Found %d OCM namespaces to audit (production and staging)\n", len(namespaces))

	results := &auditResults{
		MgmtClusterID:     a.mgmtClusterID,
		NeedsLabelRemoval: []hostedClusterAuditInfo{},
		ReadyForMigration: []hostedClusterAuditInfo{},
		AlreadyConfigured: []hostedClusterAuditInfo{},
		Errors:            []auditError{},
	}

	for _, ns := range namespaces {
		info, err := a.auditNamespaceForCluster(ctx, mgmtClient, ns.Name)
		if err != nil {
			results.Errors = append(results.Errors, auditError{
				Namespace: ns.Name,
				Error:     err.Error(),
			})
			continue
		}

		_, hasOverride := info.Annotations[annotationClusterSizeOverride]
		autoScaling, hasAutoScaling := info.Annotations[annotationResourceBasedAutoscaling]
		hasAutoscalingEnabled := hasAutoScaling && autoScaling == "true"

		if hasOverride {
			results.NeedsLabelRemoval = append(results.NeedsLabelRemoval, *info)
			if hasAutoscalingEnabled {
				results.AlreadyConfigured = append(results.AlreadyConfigured, *info)
			} else {
				results.ReadyForMigration = append(results.ReadyForMigration, *info)
			}
		} else {
			if hasAutoscalingEnabled {
				results.AlreadyConfigured = append(results.AlreadyConfigured, *info)
			} else {
				results.ReadyForMigration = append(results.ReadyForMigration, *info)
			}
		}
	}

	uniqueClusters := make(map[string]bool)
	for _, info := range results.NeedsLabelRemoval {
		uniqueClusters[info.ClusterID] = true
	}
	for _, info := range results.ReadyForMigration {
		uniqueClusters[info.ClusterID] = true
	}
	for _, info := range results.AlreadyConfigured {
		uniqueClusters[info.ClusterID] = true
	}
	results.TotalScanned = len(uniqueClusters)

	if a.showOnly != "" {
		results = a.applyFilter(results)
	}

	return a.outputResults(results)
}

// getManagementClustersFromFleet queries the fleet management API to get all management clusters.
func getManagementClustersFromFleet(conn *sdk.Connection) ([]string, error) {
	response, err := conn.OSDFleetMgmt().V1().ManagementClusters().List().Send()
	if err != nil {
		return nil, fmt.Errorf("failed to query fleet management API: %v", err)
	}

	var clusterIDs []string
	items := response.Items()
	for i := 0; i < items.Len(); i++ {
		cluster := items.Get(i)
		clusterIDs = append(clusterIDs, cluster.Name())
	}

	if len(clusterIDs) == 0 {
		return nil, fmt.Errorf("no management clusters found in fleet")
	}

	return clusterIDs, nil
}

// auditSingleManagementCluster audits a single management cluster and returns fleet cluster info.
func (a *auditOpts) auditSingleManagementCluster(ctx context.Context, conn *sdk.Connection, mgmtClusterID string) ([]fleetClusterInfo, error) {
	if err := utils.IsValidClusterKey(mgmtClusterID); err != nil {
		return nil, err
	}

	cluster, err := utils.GetCluster(conn, mgmtClusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %v", err)
	}

	isMC, err := utils.IsManagementCluster(cluster.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to verify management cluster: %v", err)
	}
	if !isMC {
		return nil, fmt.Errorf("cluster %s is not a management cluster", cluster.ID())
	}

	resolvedMgmtClusterID := cluster.ID()
	resolvedMgmtClusterName := cluster.Name()

	scheme := runtime.NewScheme()
	if err := hypershiftv1beta1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add hypershift scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core v1 scheme: %v", err)
	}

	a.k8sClientMutex.Lock()
	mgmtClient, err := k8s.New(resolvedMgmtClusterID, client.Options{Scheme: scheme})
	a.k8sClientMutex.Unlock()
	if err != nil {
		return nil, fmt.Errorf("failed to create management cluster client: %v", err)
	}

	var namespaces []corev1.Namespace
	maxRetries := 3
	retryDelay := 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		namespaces, err = a.listOcmNamespacesForCluster(ctx, mgmtClient)
		if err == nil {
			break
		}

		if attempt == maxRetries {
			return nil, fmt.Errorf("failed to list namespaces after %d attempts (cluster may be unreachable): %v", maxRetries, err)
		}

		time.Sleep(retryDelay)
	}

	var fleetClusters []fleetClusterInfo

	for _, ns := range namespaces {
		info, err := a.auditNamespaceForCluster(ctx, mgmtClient, ns.Name)
		if err != nil {
			fmt.Printf("    Warning: failed to audit namespace %s: %v\n", ns.Name, err)
			continue
		}

		fleetCluster := convertToFleetClusterInfo(resolvedMgmtClusterName, info)
		fleetClusters = append(fleetClusters, fleetCluster)
	}

	return fleetClusters, nil
}

// convertToFleetClusterInfo converts a hostedClusterAuditInfo to fleetClusterInfo format.
func convertToFleetClusterInfo(mgmtClusterID string, info *hostedClusterAuditInfo) fleetClusterInfo {
	autoScaling, hasAutoScaling := info.Annotations[annotationResourceBasedAutoscaling]
	autoscalingEnabled := hasAutoScaling && autoScaling == "true"

	_, hasOverride := info.Annotations[annotationClusterSizeOverride]

	recommendedSize := info.Annotations[annotationRecommendedClusterSize]
	if recommendedSize == "" {
		recommendedSize = "N/A"
	}

	return fleetClusterInfo{
		ManagementClusterID:   mgmtClusterID,
		ClusterID:             info.ClusterID,
		ClusterName:           info.ClusterName,
		AutoscalingEnabled:    autoscalingEnabled,
		HasOverrideAnnotation: hasOverride,
		CurrentSize:           info.CurrentSize,
		RecommendedSize:       recommendedSize,
	}
}

// listOcmNamespacesForCluster returns OCM production and staging namespaces using provided client.
func (a *auditOpts) listOcmNamespacesForCluster(ctx context.Context, kubeClient client.Client) ([]corev1.Namespace, error) {
	nsList := &corev1.NamespaceList{}
	if err := kubeClient.List(ctx, nsList); err != nil {
		return nil, err
	}

	var filtered []corev1.Namespace
	ocmNamespacePattern := regexp.MustCompile(`^ocm-(production|staging)-[a-zA-Z0-9]+$`)

	for _, ns := range nsList.Items {
		if ocmNamespacePattern.MatchString(ns.Name) {
			filtered = append(filtered, ns)
		}
	}

	return filtered, nil
}

// listOcmNamespaces returns OCM production and staging namespaces from the management cluster.
// This is a convenience wrapper for backward compatibility.
func (a *auditOpts) listOcmNamespaces(ctx context.Context) ([]corev1.Namespace, error) {
	return a.listOcmNamespacesForCluster(ctx, a.mgmtClient)
}

// auditNamespaceForCluster analyzes a single namespace using provided client.
func (a *auditOpts) auditNamespaceForCluster(ctx context.Context, kubeClient client.Client, namespace string) (*hostedClusterAuditInfo, error) {
	hc, err := a.getHostedClusterInNamespaceForClient(ctx, kubeClient, namespace)
	if err != nil {
		return nil, err
	}

	clusterID := hc.Labels[labelClusterID]
	currentSize := hc.Labels[labelHostedClusterSize]

	category := a.categorizeCluster(hc)

	return &hostedClusterAuditInfo{
		ClusterID:   clusterID,
		ClusterName: hc.Name,
		Namespace:   namespace,
		CurrentSize: currentSize,
		Category:    category,
		Labels:      hc.Labels,
		Annotations: hc.Annotations,
	}, nil
}

// auditNamespace analyzes a single namespace and returns audit information for the hosted cluster.
// This is a convenience wrapper for backward compatibility.
func (a *auditOpts) auditNamespace(ctx context.Context, namespace string) (*hostedClusterAuditInfo, error) {
	return a.auditNamespaceForCluster(ctx, a.mgmtClient, namespace)
}

// getHostedClusterInNamespaceForClient retrieves the HostedCluster resource using provided client.
func (a *auditOpts) getHostedClusterInNamespaceForClient(ctx context.Context, kubeClient client.Client, namespace string) (*hypershiftv1beta1.HostedCluster, error) {
	hcList := &hypershiftv1beta1.HostedClusterList{}
	listOpts := []client.ListOption{client.InNamespace(namespace)}

	if err := kubeClient.List(ctx, hcList, listOpts...); err != nil {
		return nil, err
	}

	if len(hcList.Items) == 0 {
		return nil, fmt.Errorf("no HostedCluster found")
	}

	if len(hcList.Items) > 1 {
		return nil, fmt.Errorf("found %d HostedClusters, expected 1", len(hcList.Items))
	}

	return &hcList.Items[0], nil
}

// getHostedClusterInNamespace retrieves the HostedCluster resource from a namespace.
// This is a convenience wrapper for backward compatibility.
func (a *auditOpts) getHostedClusterInNamespace(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error) {
	return a.getHostedClusterInNamespaceForClient(ctx, a.mgmtClient, namespace)
}

// categorizeCluster determines the migration category for a hosted cluster.
func (a *auditOpts) categorizeCluster(hc *hypershiftv1beta1.HostedCluster) string {
	if _, hasOverride := hc.Annotations[annotationClusterSizeOverride]; hasOverride {
		return "needs-removal"
	}

	autoScaling, hasAutoScaling := hc.Annotations[annotationResourceBasedAutoscaling]
	if hasAutoScaling && autoScaling == "true" {
		return "already-configured"
	}

	return "ready-for-migration"
}

// applyFilter filters audit results based on the showOnly option.
func (a *auditOpts) applyFilter(results *auditResults) *auditResults {
	filtered := &auditResults{
		MgmtClusterID: results.MgmtClusterID,
		Errors:        results.Errors,
	}

	switch a.showOnly {
	case "needs-removal":
		filtered.NeedsLabelRemoval = results.NeedsLabelRemoval
		filtered.TotalScanned = len(results.NeedsLabelRemoval)
	case "ready-for-migration":
		filtered.ReadyForMigration = results.ReadyForMigration
		filtered.TotalScanned = len(results.ReadyForMigration)
	case "safe-to-remove-override":
		// Filter clusters where: autoscaling enabled + has override + current size matches recommended size
		var safeToRemove []hostedClusterAuditInfo
		for _, info := range results.AlreadyConfigured {
			_, hasOverride := info.Annotations[annotationClusterSizeOverride]
			recommendedSize := info.Annotations[annotationRecommendedClusterSize]
			if hasOverride && recommendedSize != "" && info.CurrentSize == recommendedSize {
				safeToRemove = append(safeToRemove, info)
			}
		}
		filtered.AlreadyConfigured = safeToRemove
		filtered.TotalScanned = len(safeToRemove)
	default:
		return results
	}

	return filtered
}

// applyFleetFilter filters fleet audit results based on the showOnly option.
func (a *auditOpts) applyFleetFilter(results *fleetAuditResults) *fleetAuditResults {
	filtered := &fleetAuditResults{
		Timestamp:               results.Timestamp,
		TotalManagementClusters: results.TotalManagementClusters,
		Clusters:                []fleetClusterInfo{},
		Errors:                  results.Errors,
	}

	for _, cluster := range results.Clusters {
		isSafeToRemoveOverride := cluster.AutoscalingEnabled &&
			cluster.HasOverrideAnnotation &&
			cluster.RecommendedSize != "" &&
			cluster.RecommendedSize != "N/A" &&
			cluster.CurrentSize == cluster.RecommendedSize

		switch a.showOnly {
		case "needs-removal":
			if cluster.HasOverrideAnnotation {
				filtered.Clusters = append(filtered.Clusters, cluster)
			}
		case "ready-for-migration":
			if !cluster.AutoscalingEnabled {
				filtered.Clusters = append(filtered.Clusters, cluster)
			}
		case "safe-to-remove-override":
			if isSafeToRemoveOverride {
				filtered.Clusters = append(filtered.Clusters, cluster)
			}
		}
	}

	filtered.TotalHostedClusters = len(filtered.Clusters)
	return filtered
}

// outputResults formats and prints audit results in the specified output format.
func (a *auditOpts) outputResults(results *auditResults) error {
	switch a.output {
	case "json":
		return a.printJSONOutput(results)
	case "yaml":
		return a.printYAMLOutput(results)
	case "csv":
		return a.printCSVOutput(results)
	default:
		return a.printTextOutput(results)
	}
}

// printTextOutput prints audit results in human-readable text format.
func (a *auditOpts) printTextOutput(results *auditResults) error {
	fmt.Printf("\nManagement Cluster: %s\n", results.MgmtClusterID)
	fmt.Printf("Total Hosted Clusters Scanned: %d\n", results.TotalScanned)
	fmt.Printf("Note: Clusters with both override and autoscaling annotations appear in multiple groups\n\n")

	if len(results.NeedsLabelRemoval) > 0 {
		fmt.Printf("=== GROUP A: Has Override Annotation (%d clusters) ===\n", len(results.NeedsLabelRemoval))
		fmt.Println("Clusters with cluster-size-override annotation (may also have autoscaling enabled):")

		p := printer.NewTablePrinter(os.Stdout, 20, 1, 3, ' ')
		if !a.noHeaders {
			p.AddRow([]string{"CLUSTER ID", "CLUSTER NAME", "NAMESPACE", "CURRENT SIZE"})
		}

		sort.Slice(results.NeedsLabelRemoval, func(i, j int) bool {
			return results.NeedsLabelRemoval[i].ClusterID < results.NeedsLabelRemoval[j].ClusterID
		})

		for _, c := range results.NeedsLabelRemoval {
			p.AddRow([]string{c.ClusterID, c.ClusterName, c.Namespace, c.CurrentSize})
		}
		p.Flush()
		fmt.Println()
	}

	if len(results.ReadyForMigration) > 0 {
		fmt.Printf("=== GROUP B: Ready for Migration (%d clusters) ===\n", len(results.ReadyForMigration))
		fmt.Println("Clusters ready for autoscaling migration (may also have cluster-size-override to remove):")

		p := printer.NewTablePrinter(os.Stdout, 20, 1, 3, ' ')
		if !a.noHeaders {
			p.AddRow([]string{"CLUSTER ID", "CLUSTER NAME", "NAMESPACE", "CURRENT SIZE"})
		}

		sort.Slice(results.ReadyForMigration, func(i, j int) bool {
			return results.ReadyForMigration[i].ClusterID < results.ReadyForMigration[j].ClusterID
		})

		for _, c := range results.ReadyForMigration {
			p.AddRow([]string{c.ClusterID, c.ClusterName, c.Namespace, c.CurrentSize})
		}
		p.Flush()
		fmt.Println()
	}

	if a.showOnly == "" && len(results.AlreadyConfigured) > 0 {
		fmt.Printf("=== Already Configured (%d clusters) ===\n", len(results.AlreadyConfigured))
		fmt.Println("Clusters with autoscaling enabled (may also have cluster-size-override to remove):")

		p := printer.NewTablePrinter(os.Stdout, 20, 1, 3, ' ')
		if !a.noHeaders {
			p.AddRow([]string{"CLUSTER ID", "CLUSTER NAME", "NAMESPACE", "CURRENT SIZE"})
		}

		sort.Slice(results.AlreadyConfigured, func(i, j int) bool {
			return results.AlreadyConfigured[i].ClusterID < results.AlreadyConfigured[j].ClusterID
		})

		for _, c := range results.AlreadyConfigured {
			p.AddRow([]string{c.ClusterID, c.ClusterName, c.Namespace, c.CurrentSize})
		}
		p.Flush()
		fmt.Println()
	}

	if len(results.Errors) > 0 {
		fmt.Printf("=== Errors (%d) ===\n", len(results.Errors))
		p := printer.NewTablePrinter(os.Stdout, 30, 1, 3, ' ')
		p.AddRow([]string{"NAMESPACE", "ERROR"})
		for _, e := range results.Errors {
			p.AddRow([]string{e.Namespace, e.Error})
		}
		p.Flush()
		fmt.Println()
	}

	fmt.Println("Summary:")
	fmt.Printf("  - Group A (Has Override Annotation): %d clusters\n", len(results.NeedsLabelRemoval))
	fmt.Printf("  - Group B (Ready for migration): %d clusters\n", len(results.ReadyForMigration))
	fmt.Printf("  - Already configured: %d clusters\n", len(results.AlreadyConfigured))
	fmt.Printf("  - Errors: %d namespaces\n", len(results.Errors))

	return nil
}

// printJSONOutput prints audit results in JSON format.
func (a *auditOpts) printJSONOutput(results *auditResults) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// printYAMLOutput prints audit results in YAML format.
func (a *auditOpts) printYAMLOutput(results *auditResults) error {
	data, err := yaml.Marshal(results)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// printCSVOutput prints audit results in CSV format.
func (a *auditOpts) printCSVOutput(results *auditResults) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	if !a.noHeaders {
		w.Write([]string{"cluster_id", "cluster_name", "namespace", "current_size", "category"})
	}

	allClusters := append(append(results.NeedsLabelRemoval, results.ReadyForMigration...), results.AlreadyConfigured...)
	for _, c := range allClusters {
		w.Write([]string{c.ClusterID, c.ClusterName, c.Namespace, c.CurrentSize, c.Category})
	}

	return nil
}

// outputFleetResults formats and prints fleet audit results.
func (a *auditOpts) outputFleetResults(results *fleetAuditResults) error {
	switch a.output {
	case "json":
		return a.printFleetJSON(results)
	case "yaml":
		return a.printFleetYAML(results)
	case "csv":
		return a.printFleetCSV(results)
	default:
		return a.printFleetTable(results)
	}
}

// printFleetTable prints fleet results in table format.
func (a *auditOpts) printFleetTable(results *fleetAuditResults) error {
	fmt.Printf("\n=== Fleet-Wide Audit Results ===\n")
	fmt.Printf("Timestamp: %s\n", results.Timestamp.Format(time.RFC3339))
	fmt.Printf("Total Management Clusters: %d\n", results.TotalManagementClusters)
	fmt.Printf("Total Hosted Clusters: %d\n\n", results.TotalHostedClusters)

	if len(results.Clusters) == 0 {
		fmt.Println("No hosted clusters found across fleet")
		return nil
	}

	sort.Slice(results.Clusters, func(i, j int) bool {
		if results.Clusters[i].ManagementClusterID != results.Clusters[j].ManagementClusterID {
			return results.Clusters[i].ManagementClusterID < results.Clusters[j].ManagementClusterID
		}
		return results.Clusters[i].ClusterName < results.Clusters[j].ClusterName
	})

	p := printer.NewTablePrinter(os.Stdout, 20, 1, 3, ' ')

	if !a.noHeaders {
		p.AddRow([]string{
			"MGMT CLUSTER NAME",
			"CLUSTER ID",
			"CLUSTER NAME",
			"AUTOSCALING",
			"HAS OVERRIDE",
			"CURRENT SIZE",
			"RECOMMENDED SIZE",
		})
	}

	for _, c := range results.Clusters {
		autoscalingStr := "❌"
		if c.AutoscalingEnabled {
			autoscalingStr = "✅"
		}

		overrideStr := "❌"
		if c.HasOverrideAnnotation {
			overrideStr = "✅"
		}

		p.AddRow([]string{
			c.ManagementClusterID,
			c.ClusterID,
			c.ClusterName,
			autoscalingStr,
			overrideStr,
			c.CurrentSize,
			c.RecommendedSize,
		})
	}

	p.Flush()
	fmt.Println()

	if len(results.Errors) > 0 {
		fmt.Printf("\n=== Errors (%d) ===\n", len(results.Errors))
		for _, e := range results.Errors {
			fmt.Printf("  • Management Cluster %s", e.ManagementClusterID)
			if e.Namespace != "" {
				fmt.Printf(" / Namespace %s", e.Namespace)
			}
			fmt.Printf(": %s\n", e.Error)
		}
		fmt.Println()
	}

	return nil
}

// printFleetJSON prints fleet results in JSON format.
func (a *auditOpts) printFleetJSON(results *fleetAuditResults) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// printFleetYAML prints fleet results in YAML format.
func (a *auditOpts) printFleetYAML(results *fleetAuditResults) error {
	data, err := yaml.Marshal(results)
	if err != nil {
		return fmt.Errorf("failed to marshal to YAML: %v", err)
	}
	fmt.Println(string(data))
	return nil
}

// printFleetCSV prints fleet results in CSV format.
func (a *auditOpts) printFleetCSV(results *fleetAuditResults) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	if !a.noHeaders {
		w.Write([]string{
			"mgmt_cluster_name",
			"cluster_id",
			"cluster_name",
			"autoscaling_enabled",
			"has_override",
			"current_size",
			"recommended_size",
		})
	}

	for _, c := range results.Clusters {
		autoscalingStr := "false"
		if c.AutoscalingEnabled {
			autoscalingStr = "true"
		}

		overrideStr := "false"
		if c.HasOverrideAnnotation {
			overrideStr = "true"
		}

		w.Write([]string{
			c.ManagementClusterID,
			c.ClusterID,
			c.ClusterName,
			autoscalingStr,
			overrideStr,
			c.CurrentSize,
			c.RecommendedSize,
		})
	}

	return nil
}

// run executes the migrate command to patch clusters with autoscaling annotations.
func (m *migrateOpts) run(ctx context.Context) error {
	if err := m.initialize(ctx); err != nil {
		return fmt.Errorf("initialization failed: %v", err)
	}
	defer m.ocmConn.Close()

	candidates, err := m.getCandidatesForMigration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get migration candidates: %v", err)
	}

	if len(candidates) == 0 {
		fmt.Println("No clusters found ready for migration")
		return nil
	}

	m.displayCandidates(candidates)

	if !m.skipConfirmation && !m.dryRun {
		if !utils.ConfirmPrompt() {
			return fmt.Errorf("migration cancelled by user")
		}
	}

	if m.dryRun {
		fmt.Println("\n[DRY RUN] No changes will be applied")
		return nil
	}

	results := m.migrateClusters(ctx, candidates)

	m.displayResults(results)

	return nil
}

// initialize validates inputs and creates OCM connections and Kubernetes clients.
func (m *migrateOpts) initialize(ctx context.Context) error {
	if err := utils.IsValidClusterKey(m.serviceClusterID); err != nil {
		return fmt.Errorf("invalid service cluster ID: %v", err)
	}
	if err := utils.IsValidClusterKey(m.mgmtClusterID); err != nil {
		return fmt.Errorf("invalid management cluster ID: %v", err)
	}

	conn, err := utils.CreateConnection()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %v", err)
	}
	m.ocmConn = conn

	serviceCluster, err := utils.GetCluster(conn, m.serviceClusterID)
	if err != nil {
		return fmt.Errorf("failed to get service cluster: %v", err)
	}

	mgmtCluster, err := utils.GetCluster(conn, m.mgmtClusterID)
	if err != nil {
		return fmt.Errorf("failed to get management cluster: %v", err)
	}

	isMC, err := utils.IsManagementCluster(mgmtCluster.ID())
	if err != nil {
		return fmt.Errorf("failed to verify management cluster: %v", err)
	}
	if !isMC {
		return fmt.Errorf("cluster %s is not a management cluster", mgmtCluster.ID())
	}

	m.serviceClusterID = serviceCluster.ID()
	m.mgmtClusterID = mgmtCluster.ID()
	m.mgmtClusterName = mgmtCluster.Name()

	fmt.Printf("Service Cluster: %s (%s)\n", serviceCluster.Name(), serviceCluster.ID())
	fmt.Printf("Management Cluster: %s (%s)\n", mgmtCluster.Name(), mgmtCluster.ID())
	fmt.Printf("ManifestWork Namespace: %s\n\n", m.mgmtClusterName)

	if err := m.createClients(ctx); err != nil {
		return err
	}

	return nil
}

// createClients initializes Kubernetes clients for service and management clusters.
// The service cluster client uses elevated permissions to patch ManifestWork resources.
func (m *migrateOpts) createClients(ctx context.Context) error {
	scheme := runtime.NewScheme()
	if err := hypershiftv1beta1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add hypershift scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add core v1 scheme: %v", err)
	}
	if err := workv1.Install(scheme); err != nil {
		return fmt.Errorf("failed to add work v1 scheme: %v", err)
	}

	serviceClient, err := k8s.NewAsBackplaneClusterAdminWithConn(
		m.serviceClusterID,
		client.Options{Scheme: scheme},
		m.ocmConn,
		m.reason,
	)
	if err != nil {
		return fmt.Errorf("failed to create service cluster client with elevated permissions: %v", err)
	}
	m.serviceClient = serviceClient

	mgmtClient, err := k8s.New(m.mgmtClusterID, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create management cluster client: %v", err)
	}
	m.mgmtClient = mgmtClient

	return nil
}

// getCandidatesForMigration audits the management cluster to find clusters ready for migration.
func (m *migrateOpts) getCandidatesForMigration(ctx context.Context) ([]hostedClusterAuditInfo, error) {
	auditOpts := &auditOpts{
		mgmtClusterID: m.mgmtClusterID,
		mgmtClient:    m.mgmtClient,
	}

	namespaces, err := auditOpts.listOcmNamespaces(ctx)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Scanning %d namespaces for migration candidates...\n", len(namespaces))

	var candidates []hostedClusterAuditInfo

	for _, ns := range namespaces {
		info, err := auditOpts.auditNamespace(ctx, ns.Name)
		if err != nil {
			fmt.Printf("Warning: failed to audit namespace %s: %v\n", ns.Name, err)
			continue
		}

		// Skip clusters that already have autoscaling enabled
		autoScaling, hasAutoScaling := info.Annotations[annotationResourceBasedAutoscaling]
		if hasAutoScaling && autoScaling == "true" {
			continue
		}

		// Include clusters that need migration (ready-for-migration or needs-removal)
		if info.Category != "already-configured" {
			candidates = append(candidates, *info)
		}
	}

	return candidates, nil
}

// migrateClusters migrates a list of candidate clusters by patching their ManifestWork resources.
func (m *migrateOpts) migrateClusters(ctx context.Context, candidates []hostedClusterAuditInfo) []migrationResult {
	results := make([]migrationResult, 0, len(candidates))

	for i, candidate := range candidates {
		fmt.Printf("\n[%d/%d] Migrating cluster %s (%s)...\n",
			i+1, len(candidates), candidate.ClusterName, candidate.ClusterID)

		result := m.migrateCluster(ctx, candidate)
		results = append(results, result)

		if result.Status == "success" {
			fmt.Printf("✓ Successfully migrated %s\n", candidate.ClusterID)
		} else {
			fmt.Printf("✗ Failed to migrate %s: %s\n", candidate.ClusterID, result.Error)
		}
	}

	return results
}

// migrateCluster migrates a single cluster by patching its ManifestWork and verifying sync.
func (m *migrateOpts) migrateCluster(ctx context.Context, info hostedClusterAuditInfo) migrationResult {
	result := migrationResult{
		ClusterID:   info.ClusterID,
		ClusterName: info.ClusterName,
	}

	if err := m.patchManifestWork(ctx, info.ClusterID); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("failed to patch ManifestWork: %v", err)
		return result
	}

	fmt.Printf("  - Patched ManifestWork on service cluster\n")

	if err := m.waitForSync(ctx, info); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("sync verification failed: %v", err)
		return result
	}

	result.Status = "success"
	result.VerifiedAt = time.Now().Format(time.RFC3339)
	return result
}

// patchManifestWork adds autoscaling annotations to the HostedCluster manifest in ManifestWork.
func (m *migrateOpts) patchManifestWork(ctx context.Context, clusterID string) error {
	manifestWork := &workv1.ManifestWork{}
	err := m.serviceClient.Get(ctx,
		types.NamespacedName{
			Name:      clusterID,
			Namespace: m.mgmtClusterName,
		},
		manifestWork)

	if err != nil {
		return fmt.Errorf("failed to get ManifestWork %s/%s: %v",
			m.mgmtClusterName, clusterID, err)
	}

	modified := false
	for i, manifest := range manifestWork.Spec.Workload.Manifests {
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

		annotations, ok := metadata["annotations"].(map[string]interface{})
		if !ok {
			annotations = make(map[string]interface{})
			metadata["annotations"] = annotations
		}

		annotations[annotationResourceBasedAutoscaling] = "true"

		jsonData, err := json.Marshal(manifestData)
		if err != nil {
			return fmt.Errorf("failed to marshal modified manifest: %v", err)
		}

		manifestWork.Spec.Workload.Manifests[i].Raw = jsonData
		modified = true
		break
	}

	if !modified {
		return fmt.Errorf("HostedCluster not found in ManifestWork manifests")
	}

	if err := m.serviceClient.Update(ctx, manifestWork); err != nil {
		return fmt.Errorf("failed to update ManifestWork: %v", err)
	}

	return nil
}

// waitForSync polls the management cluster until annotations sync or timeout occurs.
func (m *migrateOpts) waitForSync(ctx context.Context, info hostedClusterAuditInfo) error {
	const (
		pollInterval = 15 * time.Second
		timeout      = 5 * time.Minute
	)

	fmt.Printf("  - Waiting for sync (timeout: 5 minutes)...\n")

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		case <-ticker.C:
			attempt++

			hc, err := m.getHostedClusterFromMgmt(ctx, info.Namespace, info.ClusterName)
			if err != nil {
				fmt.Printf("  - Attempt %d: failed to get HostedCluster: %v\n", attempt, err)

				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for sync after %v", timeout)
				}
				continue
			}

			if m.hasRequiredAnnotations(hc) {
				fmt.Printf("  - Verified: Annotations synced to management cluster\n")
				return nil
			}

			fmt.Printf("  - Attempt %d: Annotations not yet synced\n", attempt)

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout: annotations did not sync after %v", timeout)
			}
		}
	}
}

// getHostedClusterFromMgmt retrieves a HostedCluster from the management cluster.
func (m *migrateOpts) getHostedClusterFromMgmt(ctx context.Context, namespace, name string) (*hypershiftv1beta1.HostedCluster, error) {
	hc := &hypershiftv1beta1.HostedCluster{}
	err := m.mgmtClient.Get(ctx,
		types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
		hc)
	return hc, err
}

// hasRequiredAnnotations checks if a HostedCluster has the required autoscaling annotations.
func (m *migrateOpts) hasRequiredAnnotations(hc *hypershiftv1beta1.HostedCluster) bool {
	annotations := hc.Annotations
	if annotations == nil {
		return false
	}

	autoScaling, hasAutoScaling := annotations[annotationResourceBasedAutoscaling]

	return hasAutoScaling && autoScaling == "true"
}

// displayCandidates prints the list of clusters ready for migration.
func (m *migrateOpts) displayCandidates(candidates []hostedClusterAuditInfo) {
	fmt.Printf("\n=== Clusters Ready for Migration (%d) ===\n\n", len(candidates))

	p := printer.NewTablePrinter(os.Stdout, 20, 1, 3, ' ')
	p.AddRow([]string{"CLUSTER ID", "CLUSTER NAME", "NAMESPACE", "CURRENT SIZE"})

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ClusterID < candidates[j].ClusterID
	})

	for _, c := range candidates {
		p.AddRow([]string{c.ClusterID, c.ClusterName, c.Namespace, c.CurrentSize})
	}
	p.Flush()
	fmt.Println()

	fmt.Println("These clusters will receive the following annotation:")
	fmt.Printf("  - %s: \"true\"\n", annotationResourceBasedAutoscaling)
	fmt.Println()

	// Count and warn about clusters with override annotations
	overrideCount := 0
	for _, c := range candidates {
		if _, hasOverride := c.Annotations[annotationClusterSizeOverride]; hasOverride {
			overrideCount++
		}
	}

	if overrideCount > 0 {
		fmt.Printf("⚠️  Note: %d cluster(s) have the 'cluster-size-override' annotation.\n", overrideCount)
		fmt.Printf("    The autoscaling annotation will be added, but the override will remain.\n")
		fmt.Printf("    Review and manually remove override annotations if appropriate.\n")
		fmt.Println()
	}
}

// displayResults prints a summary of the migration results.
func (m *migrateOpts) displayResults(results []migrationResult) {
	var migrated, failed []migrationResult

	for _, r := range results {
		switch r.Status {
		case "success":
			migrated = append(migrated, r)
		case "failed":
			failed = append(failed, r)
		}
	}

	fmt.Printf("\n\n=== Migration Summary ===\n\n")
	fmt.Printf("Total candidates: %d\n", len(results))
	fmt.Printf("Successfully migrated: %d\n", len(migrated))
	fmt.Printf("Failed: %d\n\n", len(failed))

	if len(migrated) > 0 {
		fmt.Println("✓ Successfully Migrated:")
		for _, r := range migrated {
			fmt.Printf("  - %s (%s)\n", r.ClusterName, r.ClusterID)
		}
		fmt.Println()
	}

	if len(failed) > 0 {
		fmt.Println("✗ Failed Migrations:")
		p := printer.NewTablePrinter(os.Stdout, 20, 1, 3, ' ')
		p.AddRow([]string{"CLUSTER ID", "CLUSTER NAME", "ERROR"})
		for _, r := range failed {
			p.AddRow([]string{r.ClusterID, r.ClusterName, r.Error})
		}
		p.Flush()
		fmt.Println()
	}
}

// run executes the rollback command to remove autoscaling annotations from a hosted cluster.
func (r *rollbackOpts) run(ctx context.Context) error {
	if err := r.initialize(ctx); err != nil {
		return fmt.Errorf("initialization failed: %v", err)
	}
	defer r.ocmConn.Close()

	fmt.Println()

	hostedCluster, namespace, err := r.findHostedCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to find hosted cluster: %v", err)
	}

	if err := r.displayRollbackInfo(hostedCluster, namespace); err != nil {
		return err
	}

	if !r.skipConfirmation && !r.dryRun {
		fmt.Println("\nDo you want to proceed with the rollback?")
		if !utils.ConfirmPrompt() {
			return fmt.Errorf("rollback cancelled by user")
		}
	}

	if r.dryRun {
		fmt.Println("\n[DRY RUN] No changes will be applied")
		return nil
	}

	result := r.rollbackCluster(ctx, hostedCluster, namespace)

	r.displayRollbackResult(result)

	if result.Status == "failed" {
		return fmt.Errorf("rollback failed: %s", result.Error)
	}

	return nil
}

// initialize validates inputs and creates OCM connections and Kubernetes clients.
func (r *rollbackOpts) initialize(ctx context.Context) error {
	if err := utils.IsValidClusterKey(r.hostedClusterID); err != nil {
		return fmt.Errorf("invalid hosted cluster ID: %v", err)
	}

	conn, err := utils.CreateConnection()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %v", err)
	}
	r.ocmConn = conn

	hostedCluster, err := utils.GetCluster(conn, r.hostedClusterID)
	if err != nil {
		return fmt.Errorf("failed to get hosted cluster %s: %v", r.hostedClusterID, err)
	}

	resolvedClusterID := hostedCluster.ID()
	r.hostedClusterID = resolvedClusterID

	fmt.Printf("Hosted Cluster: %s (%s)\n", hostedCluster.Name(), hostedCluster.ID())

	mgmtCluster, err := utils.GetManagementCluster(resolvedClusterID)
	if err != nil {
		return fmt.Errorf("failed to get management cluster for hosted cluster %s: %v", resolvedClusterID, err)
	}

	r.mgmtClusterID = mgmtCluster.ID()
	r.mgmtClusterName = mgmtCluster.Name()

	fmt.Printf("Management Cluster: %s (%s)\n", mgmtCluster.Name(), mgmtCluster.ID())

	serviceCluster, err := utils.GetServiceCluster(resolvedClusterID)
	if err != nil {
		return fmt.Errorf("failed to get service cluster for hosted cluster %s: %v", resolvedClusterID, err)
	}

	r.serviceClusterID = serviceCluster.ID()

	fmt.Printf("Service Cluster: %s (%s)\n", serviceCluster.Name(), serviceCluster.ID())

	scheme := runtime.NewScheme()
	if err := hypershiftv1beta1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add hypershift scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add core v1 scheme: %v", err)
	}
	if err := workv1.Install(scheme); err != nil {
		return fmt.Errorf("failed to add work v1 scheme: %v", err)
	}

	mgmtClient, err := k8s.New(r.mgmtClusterID, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create management cluster client: %v", err)
	}
	r.mgmtClient = mgmtClient

	serviceClient, err := k8s.NewAsBackplaneClusterAdminWithConn(
		r.serviceClusterID,
		client.Options{Scheme: scheme},
		r.ocmConn,
		r.reason,
	)
	if err != nil {
		return fmt.Errorf("failed to create service cluster client: %v", err)
	}
	r.serviceClient = serviceClient

	return nil
}

// findHostedCluster finds the hosted cluster on the management cluster.
func (r *rollbackOpts) findHostedCluster(ctx context.Context) (*hypershiftv1beta1.HostedCluster, string, error) {
	auditOpts := &auditOpts{
		mgmtClusterID: r.mgmtClusterID,
		mgmtClient:    r.mgmtClient,
	}

	namespaces, err := auditOpts.listOcmNamespaces(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list namespaces: %v", err)
	}

	for _, ns := range namespaces {
		hc, err := auditOpts.getHostedClusterInNamespace(ctx, ns.Name)
		if err != nil {
			continue
		}

		clusterID := hc.Labels[labelClusterID]
		if clusterID == r.hostedClusterID || hc.Name == r.hostedClusterID {
			return hc, ns.Name, nil
		}
	}

	return nil, "", fmt.Errorf("hosted cluster %s not found on management cluster %s", r.hostedClusterID, r.mgmtClusterID)
}

// displayRollbackInfo shows information about the cluster to be rolled back.
func (r *rollbackOpts) displayRollbackInfo(hc *hypershiftv1beta1.HostedCluster, namespace string) error {
	clusterID := hc.Labels[labelClusterID]
	currentSize := hc.Labels[labelHostedClusterSize]

	autoScaling, hasAutoScaling := hc.Annotations[annotationResourceBasedAutoscaling]

	fmt.Printf("=== Rollback Configuration ===\n\n")
	fmt.Printf("Hosted Cluster ID: %s\n", clusterID)
	fmt.Printf("Hosted Cluster Name: %s\n", hc.Name)
	fmt.Printf("Namespace: %s\n", namespace)
	fmt.Printf("Current Size: %s\n", currentSize)
	fmt.Printf("\nCurrent autoscaling annotation: %s=%s\n",
		annotationResourceBasedAutoscaling, autoScaling)

	if !hasAutoScaling || autoScaling != "true" {
		return fmt.Errorf("cluster does not have autoscaling enabled (annotation not set or not 'true')")
	}

	fmt.Printf("\nThis will remove the annotation: %s\n", annotationResourceBasedAutoscaling)

	return nil
}

// rollbackCluster removes autoscaling annotations from the cluster's ManifestWork.
func (r *rollbackOpts) rollbackCluster(ctx context.Context, hc *hypershiftv1beta1.HostedCluster, namespace string) migrationResult {
	clusterID := hc.Labels[labelClusterID]

	result := migrationResult{
		ClusterID:   clusterID,
		ClusterName: hc.Name,
	}

	if err := r.removeManifestWorkAnnotation(ctx, clusterID); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("failed to patch ManifestWork: %v", err)
		return result
	}

	fmt.Printf("\n  - Removed annotation from ManifestWork on service cluster\n")

	if err := r.waitForRollbackSync(ctx, namespace, hc.Name); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("sync verification failed: %v", err)
		return result
	}

	result.Status = "success"
	result.VerifiedAt = time.Now().Format(time.RFC3339)
	return result
}

// removeManifestWorkAnnotation removes autoscaling annotations from the HostedCluster manifest in ManifestWork.
func (r *rollbackOpts) removeManifestWorkAnnotation(ctx context.Context, clusterID string) error {
	manifestWork := &workv1.ManifestWork{}
	err := r.serviceClient.Get(ctx,
		types.NamespacedName{
			Name:      clusterID,
			Namespace: r.mgmtClusterName,
		},
		manifestWork)

	if err != nil {
		return fmt.Errorf("failed to get ManifestWork %s/%s: %v",
			r.mgmtClusterName, clusterID, err)
	}

	modified := false
	for i, manifest := range manifestWork.Spec.Workload.Manifests {
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
			continue
		}

		annotations, ok := metadata["annotations"].(map[string]interface{})
		if !ok {
			continue
		}

		delete(annotations, annotationResourceBasedAutoscaling)

		jsonData, err := json.Marshal(manifestData)
		if err != nil {
			return fmt.Errorf("failed to marshal modified manifest: %v", err)
		}

		manifestWork.Spec.Workload.Manifests[i].Raw = jsonData
		modified = true
		break
	}

	if !modified {
		return fmt.Errorf("HostedCluster not found in ManifestWork manifests")
	}

	if err := r.serviceClient.Update(ctx, manifestWork); err != nil {
		return fmt.Errorf("failed to update ManifestWork: %v", err)
	}

	return nil
}

// waitForRollbackSync polls the management cluster until annotation removal is synced or timeout occurs.
func (r *rollbackOpts) waitForRollbackSync(ctx context.Context, namespace, name string) error {
	const (
		pollInterval = 15 * time.Second
		timeout      = 5 * time.Minute
	)

	fmt.Printf("  - Waiting for sync (timeout: 5 minutes)...\n")

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		case <-ticker.C:
			attempt++

			hc := &hypershiftv1beta1.HostedCluster{}
			err := r.mgmtClient.Get(ctx,
				types.NamespacedName{
					Namespace: namespace,
					Name:      name,
				},
				hc)

			if err != nil {
				fmt.Printf("  - Attempt %d: failed to get HostedCluster: %v\n", attempt, err)

				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for sync after %v", timeout)
				}
				continue
			}

			autoScaling, hasAutoScaling := hc.Annotations[annotationResourceBasedAutoscaling]

			if !hasAutoScaling || autoScaling != "true" {
				fmt.Printf("  - Verified: Annotation removed from management cluster\n")
				return nil
			}

			fmt.Printf("  - Attempt %d: Annotation still present\n", attempt)

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout: annotation was not removed after %v", timeout)
			}
		}
	}
}

// displayRollbackResult prints the result of the rollback operation.
func (r *rollbackOpts) displayRollbackResult(result migrationResult) {
	fmt.Printf("\n=== Rollback Result ===\n\n")

	if result.Status == "success" {
		fmt.Printf("✓ Successfully rolled back cluster %s (%s)\n", result.ClusterName, result.ClusterID)
		fmt.Printf("  Verified at: %s\n", result.VerifiedAt)
	} else {
		fmt.Printf("✗ Failed to rollback cluster %s (%s)\n", result.ClusterName, result.ClusterID)
		fmt.Printf("  Error: %s\n", result.Error)
	}
}
