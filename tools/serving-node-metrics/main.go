package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/openshift/osdctl/cmd/dynatrace"
	"github.com/spf13/cobra"
)

const (
	authURL       = "https://sso.dynatrace.com/sso/oauth2/token"
	storageScopes = "storage:logs:read storage:events:read storage:buckets:read storage:metrics:read"
)

var (
	vaultAddr string
	vaultPath string
)

type NodeMetrics struct {
	ClusterID string
	Node      string
	CPU       float64
	Memory    float64
	DiskIn    float64
	DiskOut   float64
	NetIn     float64
	NetOut    float64
}

func main() {
	cmd := &cobra.Command{
		Use:   "dynatrace-metrics CLUSTER_ID [CLUSTER_ID...]",
		Short: "Get request-serving node metrics from Dynatrace",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runMetrics,
	}

	cmd.Flags().IntP("hours", "H", 1, "Time window in hours to query")
	cmd.Flags().StringVar(&vaultAddr, "vault-addr", "", "Vault server address (required, or set VAULT_ADDR env var)")
	cmd.Flags().StringVar(&vaultPath, "vault-path", "", "Vault secret path for Dynatrace credentials (required)")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runMetrics(cmd *cobra.Command, args []string) error {
	clusterIDs := args
	hours, _ := cmd.Flags().GetInt("hours")

	fmt.Println("==================================================")
	fmt.Println("Request-Serving Node Metrics Report")
	fmt.Println("==================================================")
	fmt.Printf("Clusters: %d\n", len(clusterIDs))
	fmt.Printf("Time Window: Last %d hour(s)\n\n", hours)

	// Get access token once for all clusters
	fmt.Println("[1/3] Authenticating with Dynatrace...")
	accessToken, err := getAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}
	fmt.Println("✓ Authenticated")

	// Collect metrics for all clusters
	fmt.Println("[2/3] Querying metrics for all clusters...")
	allMetrics := []NodeMetrics{}

	for i, clusterID := range clusterIDs {
		fmt.Printf("\n  Cluster %d/%d: %s\n", i+1, len(clusterIDs), clusterID)

		// Get cluster details
		cluster, err := dynatrace.FetchClusterDetails(clusterID)
		if err != nil {
			fmt.Printf("  ⚠ Failed to get cluster details: %v\n", err)
			continue
		}

		// Get external ID
		externalID, err := getExternalID(clusterID)
		if err != nil {
			fmt.Printf("  ⚠ Failed to get external ID: %v\n", err)
			continue
		}

		// Query all metrics for this cluster
		metrics, err := queryClusterMetrics(cluster.DynatraceURL, accessToken, clusterID, externalID, hours)
		if err != nil {
			fmt.Printf("  ⚠ Failed to query metrics: %v\n", err)
			continue
		}

		allMetrics = append(allMetrics, metrics...)
		fmt.Printf("  ✓ Collected metrics for %d nodes\n", len(metrics))
	}

	// Display results in table
	fmt.Println("\n[3/3] Results")
	fmt.Println("==================================================")

	if len(allMetrics) == 0 {
		fmt.Println("No metrics data collected.")
		return nil
	}

	displayMetricsTable(allMetrics)

	return nil
}

func getExternalID(clusterID string) (string, error) {
	out, err := exec.Command("ocm", "describe", "cluster", clusterID, "--json").Output()
	if err != nil {
		return "", err
	}

	var result struct {
		ExternalID string `json:"external_id"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return "", err
	}

	return result.ExternalID, nil
}

func getAccessToken() (string, error) {
	// Get OAuth credentials from Vault
	clientID, clientSecret, err := getVaultCredentials()
	if err != nil {
		return "", err
	}

	// Exchange for access token
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"scope":         {storageScopes},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}

	resp, err := http.PostForm(authURL, data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	return tokenResp.AccessToken, nil
}

func getVaultCredentials() (string, string, error) {
	// Use env var if flag not set
	if vaultAddr == "" {
		vaultAddr = os.Getenv("VAULT_ADDR")
	}
	if vaultAddr == "" {
		return "", "", fmt.Errorf("vault address required: set --vault-addr flag or VAULT_ADDR env var")
	}
	if vaultPath == "" {
		return "", "", fmt.Errorf("vault path required: set --vault-path flag")
	}
	os.Setenv("VAULT_ADDR", vaultAddr)

	// Check if authenticated with vault
	if err := exec.Command("vault", "token", "lookup").Run(); err != nil {
		fmt.Println("Not authenticated with vault, logging in...")
		loginCmd := exec.Command("vault", "login", "-method=oidc", "-no-print")
		loginCmd.Stdout = os.Stdout
		loginCmd.Stderr = os.Stderr
		loginCmd.Stdin = os.Stdin
		if err := loginCmd.Run(); err != nil {
			return "", "", fmt.Errorf("vault login failed: %w", err)
		}
	}

	// Get secret from vault
	out, err := exec.Command("vault", "kv", "get", "-format=json", vaultPath).Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to get vault secret: %w", err)
	}

	var vaultResp struct {
		Data struct {
			Data struct {
				ID     string `json:"id"`
				Secret string `json:"secret"`
			} `json:"data"`
		} `json:"data"`
	}

	if err := json.Unmarshal(out, &vaultResp); err != nil {
		return "", "", err
	}

	return vaultResp.Data.Data.ID, vaultResp.Data.Data.Secret, nil
}

func queryMetric(dtURL, token, metric, label, externalID string, hours int) error {
	fmt.Printf("📊 %s\n", label)
	fmt.Println("---")

	// Build DQL query
	query := fmt.Sprintf("timeseries %s=avg(%s),by:{_id, node, instance_type} | filter _id == \"%s\"",
		metric, metric, externalID)

	// Execute query
	reqToken, err := executeQuery(dtURL, token, query, hours)
	if err != nil {
		return err
	}

	// Poll for results
	result, err := pollResults(dtURL, token, reqToken)
	if err != nil {
		return err
	}

	// Parse and display
	return displayResults(result, metric)
}

func executeQuery(dtURL, token, query string, hours int) (string, error) {
	// Calculate timeframe
	now := time.Now()
	end := now.Format(time.RFC3339)
	start := now.Add(time.Duration(-hours) * time.Hour).Format(time.RFC3339)

	payload := map[string]interface{}{
		"query":                 query,
		"maxResultRecords":      10000,
		"defaultTimeframeStart": start,
		"defaultTimeframeEnd":   end,
	}

	jsonData, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", dtURL+"platform/storage/query/v1/query:execute", strings.NewReader(string(jsonData)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var execResp struct {
		RequestToken string `json:"requestToken"`
		State        string `json:"state"`
	}

	if err := json.Unmarshal(body, &execResp); err != nil {
		return "", err
	}

	if execResp.RequestToken == "" {
		return "", fmt.Errorf("no request token returned: %s", string(body))
	}

	return execResp.RequestToken, nil
}

func pollResults(dtURL, token, requestToken string) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)

		req, _ := http.NewRequest("GET",
			fmt.Sprintf("%splatform/storage/query/v1/query:poll?request-token=%s", dtURL, url.QueryEscape(requestToken)),
			nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var pollResp struct {
			State string `json:"state"`
		}

		json.Unmarshal(body, &pollResp)

		if pollResp.State == "SUCCEEDED" {
			return string(body), nil
		} else if pollResp.State != "RUNNING" {
			return "", fmt.Errorf("query failed with state: %s", pollResp.State)
		}
	}

	return "", fmt.Errorf("query timed out")
}

func displayResults(result, metric string) error {
	var pollResp struct {
		Result struct {
			Records []map[string]interface{} `json:"records"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(result), &pollResp); err != nil {
		return err
	}

	records := pollResp.Result.Records
	if len(records) == 0 {
		fmt.Println("  No data available")
		return nil
	}

	// Calculate per-node averages and overall average
	var overallSum float64
	nodeAverages := make(map[string]float64)
	nodeNames := make([]string, 0, len(records))

	for _, record := range records {
		node, _ := record["node"].(string)
		nodeNames = append(nodeNames, node)

		// Handle time series array
		var nodeAvg float64
		if valArray, ok := record[metric].([]interface{}); ok {
			var sum float64
			var count int
			for _, v := range valArray {
				if fv, ok := v.(float64); ok {
					sum += fv
					count++
				}
			}
			if count > 0 {
				nodeAvg = sum / float64(count)
			}
		} else if val, ok := record[metric].(float64); ok {
			nodeAvg = val
		}

		nodeAverages[node] = nodeAvg
		overallSum += nodeAvg
	}

	overallAvg := overallSum / float64(len(records))

	fmt.Printf("  Average: %.4f\n", overallAvg)
	fmt.Printf("  Nodes: %d\n\n", len(records))

	fmt.Println("  Per-node:")
	for i, node := range nodeNames {
		if i >= 10 {
			fmt.Printf("  ... and %d more nodes\n", len(records)-10)
			break
		}
		fmt.Printf("    %s: %.4f\n", node, nodeAverages[node])
	}
	fmt.Println()

	return nil
}

func queryClusterMetrics(dtURL, token, clusterID, externalID string, hours int) ([]NodeMetrics, error) {
	metrics := []string{
		"hypershift_node_consumption_cpu",
		"hypershift_node_consumption_memory",
		"hypershift_node_consumption_disk_io_in",
		"hypershift_node_consumption_disk_io_out",
		"hypershift_node_consumption_network_in",
		"hypershift_node_consumption_network_out",
	}

	// Map to store metrics per node
	nodeData := make(map[string]*NodeMetrics)

	for _, metric := range metrics {
		// Build DQL query
		query := fmt.Sprintf("timeseries %s=avg(%s),by:{_id, node, instance_type} | filter _id == \"%s\"",
			metric, metric, externalID)

		// Execute query
		reqToken, err := executeQuery(dtURL, token, query, hours)
		if err != nil {
			continue
		}

		// Poll for results
		result, err := pollResults(dtURL, token, reqToken)
		if err != nil {
			continue
		}

		// Parse results
		var pollResp struct {
			Result struct {
				Records []map[string]interface{} `json:"records"`
			} `json:"result"`
		}

		if err := json.Unmarshal([]byte(result), &pollResp); err != nil {
			continue
		}

		// Process each node's data
		for _, record := range pollResp.Result.Records {
			node, _ := record["node"].(string)
			if node == "" {
				continue
			}

			// Initialize node if not exists
			if nodeData[node] == nil {
				nodeData[node] = &NodeMetrics{
					ClusterID: clusterID,
					Node:      node,
				}
			}

			// Calculate average for this metric
			var avg float64
			if valArray, ok := record[metric].([]interface{}); ok {
				var sum float64
				var count int
				for _, v := range valArray {
					if fv, ok := v.(float64); ok {
						sum += fv
						count++
					}
				}
				if count > 0 {
					avg = sum / float64(count)
				}
			} else if val, ok := record[metric].(float64); ok {
				avg = val
			}

			// Store the average in the appropriate field
			switch metric {
			case "hypershift_node_consumption_cpu":
				nodeData[node].CPU = avg
			case "hypershift_node_consumption_memory":
				nodeData[node].Memory = avg
			case "hypershift_node_consumption_disk_io_in":
				nodeData[node].DiskIn = avg
			case "hypershift_node_consumption_disk_io_out":
				nodeData[node].DiskOut = avg
			case "hypershift_node_consumption_network_in":
				nodeData[node].NetIn = avg
			case "hypershift_node_consumption_network_out":
				nodeData[node].NetOut = avg
			}
		}
	}

	// Convert map to slice
	result := make([]NodeMetrics, 0, len(nodeData))
	for _, nm := range nodeData {
		result = append(result, *nm)
	}

	return result, nil
}

func displayMetricsTable(metrics []NodeMetrics) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Cluster ID", "Node", "CPU %", "Memory %", "Disk I/O In", "Disk I/O Out", "Net In", "Net Out"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	for _, m := range metrics {
		table.Append([]string{
			m.ClusterID,
			m.Node,
			fmt.Sprintf("%.2f%%", m.CPU*100),
			fmt.Sprintf("%.2f%%", m.Memory*100),
			fmt.Sprintf("%.6f", m.DiskIn),
			fmt.Sprintf("%.6f", m.DiskOut),
			fmt.Sprintf("%.6f", m.NetIn),
			fmt.Sprintf("%.6f", m.NetOut),
		})
	}

	table.Render()
}
