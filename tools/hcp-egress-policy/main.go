package main

import (
	"context"
	"fmt"
	"os"
	"time"

	sdk "github.com/openshift-online/ocm-sdk-go"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/osdctl/pkg/k8s"
	"github.com/openshift/osdctl/pkg/utils"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type egressOpts struct {
	hostedClusterID  string
	elevationReason  string
	all              bool
	dryRun           bool
	skipConfirmation bool
	serviceClient    client.Client
	ocmConn          *sdk.Connection
	serviceClusterID string
	mgmtClusterName  string
}

type result struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	VerifiedAt  string `json:"verified_at,omitempty"`
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "hcp-egress-policy",
		Short: "Set egress policy label on a hosted cluster",
		Long: `A tool for setting the egress policy label on a hosted cluster by patching its ManagedCluster resource.

This tool will:
1. Look up the service cluster from OCM using the hosted cluster ID
2. Connect to the service cluster with elevated permissions
3. Add the label api.openshift.com/hosted-cluster-egress-policy: NoEgress to the ManagedCluster`,
		Example: `
  # Set egress policy for a single hosted cluster
  hcp-egress-policy --cluster-id 1a2b3c4d5e6f7g8h9i0j --elevation-reason https://issues.redhat.com/browse/SREP-3303

  # Set egress policy for all clusters with zero_egress='true' property
  hcp-egress-policy --all --elevation-reason https://issues.redhat.com/browse/SREP-3303

  # Dry run to see what would be changed
  hcp-egress-policy --cluster-id cluster-123 --elevation-reason https://issues.redhat.com/browse/SREP-3303 --dry-run

  # Process all clusters and skip confirmation prompt (use with caution)
  hcp-egress-policy --all --elevation-reason https://issues.redhat.com/browse/SREP-3303 --skip-confirmation`,
		Args:              cobra.NoArgs,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := &egressOpts{}
			opts.hostedClusterID, _ = cmd.Flags().GetString("cluster-id")
			opts.elevationReason, _ = cmd.Flags().GetString("elevation-reason")
			opts.all, _ = cmd.Flags().GetBool("all")
			opts.dryRun, _ = cmd.Flags().GetBool("dry-run")
			opts.skipConfirmation, _ = cmd.Flags().GetBool("skip-confirmation")

			// Validate flags
			if opts.all && opts.hostedClusterID != "" {
				return fmt.Errorf("cannot specify both --all and --cluster-id")
			}
			if !opts.all && opts.hostedClusterID == "" {
				return fmt.Errorf("must specify either --all or --cluster-id")
			}

			return opts.run(context.Background())
		},
	}

	rootCmd.Flags().String("cluster-id", "", "The hosted cluster ID/name/external-id to configure")
	rootCmd.Flags().String("elevation-reason", "", "Reason for elevation (Jira ticket URL)")
	rootCmd.Flags().Bool("all", false, "Process all clusters with zero_egress='true' property")
	rootCmd.Flags().Bool("dry-run", false, "Preview changes without applying them")
	rootCmd.Flags().Bool("skip-confirmation", false, "Skip confirmation prompt (use with caution)")

	_ = rootCmd.MarkFlagRequired("elevation-reason")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// run executes the main logic to set the egress policy label.
func (e *egressOpts) run(ctx context.Context) error {
	conn, err := utils.CreateConnection()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %v", err)
	}
	e.ocmConn = conn
	defer e.ocmConn.Close()

	if e.all {
		return e.runAllClusters(ctx)
	}
	return e.runSingleCluster(ctx)
}

// runSingleCluster processes a single cluster.
func (e *egressOpts) runSingleCluster(ctx context.Context) error {
	if err := e.initializeSingleCluster(ctx); err != nil {
		return fmt.Errorf("initialization failed: %v", err)
	}

	fmt.Println()

	if err := e.displayPatchInfo(ctx); err != nil {
		return err
	}

	if !e.skipConfirmation && !e.dryRun {
		fmt.Println("\nDo you want to proceed with setting the egress policy?")
		if !utils.ConfirmPrompt() {
			return fmt.Errorf("operation cancelled by user")
		}
	}

	if e.dryRun {
		fmt.Println("\n[DRY RUN] No changes will be applied")
		return nil
	}

	result := e.patchCluster(ctx)

	e.displayResult(result)

	if result.Status == "failed" {
		return fmt.Errorf("patch failed: %s", result.Error)
	}

	return nil
}

// runAllClusters processes all clusters with zero_egress='true' property.
func (e *egressOpts) runAllClusters(ctx context.Context) error {
	clusters, err := e.getClustersFromOCM()
	if err != nil {
		return fmt.Errorf("failed to get clusters from OCM: %v", err)
	}

	if len(clusters) == 0 {
		fmt.Println("No clusters found with zero_egress='true' property")
		return nil
	}

	fmt.Printf("\nFound %d cluster(s) with zero_egress='true' property:\n\n", len(clusters))
	for i, cluster := range clusters {
		fmt.Printf("%d. %s (%s)\n", i+1, cluster.Name(), cluster.ID())
	}

	if !e.skipConfirmation && !e.dryRun {
		fmt.Printf("\nDo you want to proceed with setting egress policy for all %d cluster(s)?\n", len(clusters))
		if !utils.ConfirmPrompt() {
			return fmt.Errorf("operation cancelled by user")
		}
	}

	if e.dryRun {
		fmt.Println("\n[DRY RUN] No changes will be applied")
		return nil
	}

	results := make([]result, 0, len(clusters))
	successCount := 0
	failureCount := 0

	for i, cluster := range clusters {
		fmt.Printf("\n[%d/%d] Processing cluster %s (%s)...\n", i+1, len(clusters), cluster.Name(), cluster.ID())

		e.hostedClusterID = cluster.ID()

		if err := e.initializeSingleCluster(ctx); err != nil {
			fmt.Printf("✗ Failed to initialize: %v\n", err)
			results = append(results, result{
				ClusterID:   cluster.ID(),
				ClusterName: cluster.Name(),
				Status:      "failed",
				Error:       fmt.Sprintf("initialization failed: %v", err),
			})
			failureCount++
			continue
		}

		res := e.patchCluster(ctx)
		results = append(results, res)

		if res.Status == "success" {
			fmt.Printf("✓ Successfully set egress policy for %s\n", cluster.Name())
			successCount++
		} else {
			fmt.Printf("✗ Failed to set egress policy for %s: %s\n", cluster.Name(), res.Error)
			failureCount++
		}
	}

	e.displayAllResults(results, successCount, failureCount)

	return nil
}

// getClustersFromOCM fetches clusters with zero_egress='true' property from OCM.
func (e *egressOpts) getClustersFromOCM() ([]*cmv1.Cluster, error) {
	request := e.ocmConn.ClustersMgmt().V1().Clusters().List().Search("properties.zero_egress='true'")

	response, err := request.Send()
	if err != nil {
		return nil, fmt.Errorf("failed to search clusters: %v", err)
	}

	clusters := make([]*cmv1.Cluster, 0, response.Size())
	response.Items().Each(func(cluster *cmv1.Cluster) bool {
		clusters = append(clusters, cluster)
		return true
	})

	return clusters, nil
}

// initializeSingleCluster validates inputs and creates a Kubernetes client for the service cluster.
func (e *egressOpts) initializeSingleCluster(ctx context.Context) error {
	if err := utils.IsValidClusterKey(e.hostedClusterID); err != nil {
		return fmt.Errorf("invalid hosted cluster ID: %v", err)
	}

	hostedCluster, err := utils.GetCluster(e.ocmConn, e.hostedClusterID)
	if err != nil {
		return fmt.Errorf("failed to get hosted cluster %s: %v", e.hostedClusterID, err)
	}

	resolvedClusterID := hostedCluster.ID()
	e.hostedClusterID = resolvedClusterID

	fmt.Printf("Hosted Cluster: %s (%s)\n", hostedCluster.Name(), hostedCluster.ID())

	mgmtCluster, err := utils.GetManagementCluster(resolvedClusterID)
	if err != nil {
		return fmt.Errorf("failed to get management cluster for hosted cluster %s: %v", resolvedClusterID, err)
	}

	e.mgmtClusterName = mgmtCluster.Name()

	fmt.Printf("Management Cluster: %s (%s)\n", mgmtCluster.Name(), mgmtCluster.ID())

	serviceCluster, err := utils.GetServiceCluster(resolvedClusterID)
	if err != nil {
		return fmt.Errorf("failed to get service cluster for hosted cluster %s: %v", resolvedClusterID, err)
	}

	e.serviceClusterID = serviceCluster.ID()

	fmt.Printf("Service Cluster: %s (%s)\n", serviceCluster.Name(), serviceCluster.ID())

	scheme := runtime.NewScheme()
	if err := clusterv1.Install(scheme); err != nil {
		return fmt.Errorf("failed to add cluster v1 scheme: %v", err)
	}

	serviceClient, err := k8s.NewAsBackplaneClusterAdminWithConn(
		e.serviceClusterID,
		client.Options{Scheme: scheme},
		e.ocmConn,
		e.elevationReason,
	)
	if err != nil {
		return fmt.Errorf("failed to create service cluster client: %v", err)
	}
	e.serviceClient = serviceClient

	return nil
}

// displayPatchInfo shows information about the cluster to be patched.
func (e *egressOpts) displayPatchInfo(ctx context.Context) error {
	mc := &clusterv1.ManagedCluster{}
	err := e.serviceClient.Get(ctx,
		types.NamespacedName{Name: e.hostedClusterID},
		mc)
	if err != nil {
		return fmt.Errorf("failed to get ManagedCluster %s: %v", e.hostedClusterID, err)
	}

	fmt.Printf("=== Egress Policy Configuration ===\n\n")
	fmt.Printf("Hosted Cluster ID: %s\n", e.hostedClusterID)
	fmt.Printf("ManagedCluster Namespace: %s\n", e.mgmtClusterName)

	currentEgressPolicy, hasEgressPolicy := mc.Labels["api.openshift.com/hosted-cluster-egress-policy"]
	if hasEgressPolicy {
		fmt.Printf("\nCurrent egress policy label: %s\n", currentEgressPolicy)
		if currentEgressPolicy == "NoEgress" {
			fmt.Printf("\nWARNING: Egress policy is already set to NoEgress\n")
		}
	} else {
		fmt.Printf("\nCurrent egress policy label: (not set)\n")
	}

	fmt.Printf("\nThis will set the label: api.openshift.com/hosted-cluster-egress-policy=NoEgress\n")

	return nil
}

// patchCluster adds the egress policy label to the cluster's ManagedCluster.
func (e *egressOpts) patchCluster(ctx context.Context) result {
	res := result{
		ClusterID: e.hostedClusterID,
	}

	if err := e.patchManagedClusterLabel(ctx, e.hostedClusterID); err != nil {
		res.Status = "failed"
		res.Error = fmt.Sprintf("failed to patch ManagedCluster: %v", err)
		return res
	}

	fmt.Printf("\n  - Patched ManagedCluster on service cluster\n")

	res.Status = "success"
	res.VerifiedAt = time.Now().Format(time.RFC3339)
	return res
}

// patchManagedClusterLabel adds the egress policy label to the ManagedCluster resource.
func (e *egressOpts) patchManagedClusterLabel(ctx context.Context, clusterID string) error {
	mc := &clusterv1.ManagedCluster{}
	err := e.serviceClient.Get(ctx,
		types.NamespacedName{Name: clusterID},
		mc)
	if err != nil {
		return fmt.Errorf("failed to get ManagedCluster %s: %v", clusterID, err)
	}

	if mc.Labels == nil {
		mc.Labels = make(map[string]string)
	}

	mc.Labels["api.openshift.com/hosted-cluster-egress-policy"] = "NoEgress"

	if err := e.serviceClient.Update(ctx, mc); err != nil {
		return fmt.Errorf("failed to update ManagedCluster: %v", err)
	}

	return nil
}

// displayResult prints the result of the patch operation.
func (e *egressOpts) displayResult(res result) {
	fmt.Printf("\n=== Operation Result ===\n\n")

	if res.Status == "success" {
		fmt.Printf("✓ Successfully set egress policy for cluster %s (%s)\n", res.ClusterName, res.ClusterID)
		fmt.Printf("  Verified at: %s\n", res.VerifiedAt)
	} else {
		fmt.Printf("✗ Failed to set egress policy for cluster %s (%s)\n", res.ClusterName, res.ClusterID)
		fmt.Printf("  Error: %s\n", res.Error)
	}
}

// displayAllResults shows a summary of results for all processed clusters.
func (e *egressOpts) displayAllResults(results []result, successCount, failureCount int) {
	fmt.Printf("\n\n=== Summary ===\n\n")
	fmt.Printf("Total clusters: %d\n", len(results))
	fmt.Printf("Successful: %d\n", successCount)
	fmt.Printf("Failed: %d\n", failureCount)

	if successCount > 0 {
		fmt.Printf("\n✓ Successfully configured clusters:\n")
		for _, res := range results {
			if res.Status == "success" {
				fmt.Printf("  - %s (%s)\n", res.ClusterName, res.ClusterID)
			}
		}
	}

	if failureCount > 0 {
		fmt.Printf("\n✗ Failed clusters:\n")
		for _, res := range results {
			if res.Status == "failed" {
				fmt.Printf("  - %s (%s): %s\n", res.ClusterName, res.ClusterID, res.Error)
			}
		}
	}
}
