# HCP Node Autoscaling Tool

A tool to audit and migrate hosted clusters to resource-based node autoscaling.

## Overview

This tool provides three subcommands:

1. **audit**: Analyzes hosted clusters and categorizes them based on autoscaling migration readiness
2. **migrate**: Patches ManifestWork resources to enable resource-based node autoscaling
3. **rollback**: Removes autoscaling annotations from a hosted cluster by patching its ManifestWork

The tool inspects cluster annotations and can automatically migrate clusters that are ready for autoscaling, or rollback clusters that were previously migrated.

## Installation

### From Source

```bash
git clone https://github.com/openshift-online/rosa-hcp-platform-tools.git
cd rosa-hcp-platform-tools/tools/hcp-node-autoscaling
go build -o hcp-node-autoscaling .
```

### Binary

The compiled binary will be created in the current directory as `hcp-node-autoscaling`.

## Usage

### Audit Command

The audit command analyzes clusters and reports their autoscaling readiness. It supports two modes:

1. **Single Cluster Mode**: Audits one management cluster with detailed categorization
2. **Fleet Mode**: Audits all management clusters with a flattened view of all hosted clusters

#### Single Cluster Mode

##### Basic Audit

```bash
hcp-node-autoscaling audit --mgmt-cluster-id <MANAGEMENT_CLUSTER_ID>
```

##### Output Formats

**Text (Default)**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123
```

**JSON**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --output json
```

**CSV**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --output csv
```

**YAML**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --output yaml
```

##### Filtering Results

**Show only clusters that need annotation removal**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --show-only needs-removal
```

**Show only clusters ready for migration**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --show-only ready-for-migration
```

**Show only clusters safe to remove override (autoscaling enabled, has override, sizes match)**
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --show-only safe-to-remove-override
```

#### Fleet Mode

Fleet mode queries all management clusters and outputs a single table showing autoscaling status across the entire fleet.

##### Basic Fleet Audit

```bash
hcp-node-autoscaling audit --fleet
```

##### Fleet Audit with Different Output Formats

**CSV for spreadsheet analysis**
```bash
hcp-node-autoscaling audit --fleet --output csv > fleet-audit.csv
```

**JSON for scripting**
```bash
hcp-node-autoscaling audit --fleet --output json > fleet-audit.json
```

**YAML**
```bash
hcp-node-autoscaling audit --fleet --output yaml
```

##### Fleet Audit without Headers

```bash
hcp-node-autoscaling audit --fleet --no-headers
```

##### Fleet Audit with Filtering

**Show only clusters ready for migration**
```bash
hcp-node-autoscaling audit --fleet --show-only ready-for-migration
```

**Show only clusters that need annotation removal**
```bash
hcp-node-autoscaling audit --fleet --show-only needs-removal
```

**Show only clusters safe to remove override**
```bash
hcp-node-autoscaling audit --fleet --show-only safe-to-remove-override
```

### Migrate Command

The migrate command automatically patches clusters that are ready for autoscaling migration.

#### Basic Migration

```bash
hcp-node-autoscaling migrate \
  --service-cluster-id <SERVICE_CLUSTER_ID> \
  --mgmt-cluster-id <MANAGEMENT_CLUSTER_ID> \
  --reason "Enabling request serving node autoscaling"
```

#### Dry Run

Preview what would be migrated without making changes:

```bash
hcp-node-autoscaling migrate \
  --service-cluster-id svc-123 \
  --mgmt-cluster-id mgmt-456 \
  --reason "Enabling request serving node autoscaling" \
  --dry-run
```

#### Skip Confirmation

Skip the confirmation prompt (use with caution):

```bash
hcp-node-autoscaling migrate \
  --service-cluster-id svc-123 \
  --mgmt-cluster-id mgmt-456 \
  --reason "Enabling request serving node autoscaling" \
  --skip-confirmation
```

### Rollback Command

The rollback command removes autoscaling annotations from a previously migrated hosted cluster.

The `--hosted-cluster-id` flag accepts:
- Cluster name
- Internal cluster ID
- External cluster ID

#### Basic Rollback

```bash
# Using internal cluster ID
hcp-node-autoscaling rollback \
  --hosted-cluster-id 1a2b3c4d5e6f7g8h9i0j \
  --reason "Rolling back hosted cluster autoscaling migration"
```

#### Dry Run

Preview what would be changed without making changes:

```bash
hcp-node-autoscaling rollback \
  --hosted-cluster-id cluster-123 \
  --reason "Rolling back hosted cluster autoscaling migration" \
  --dry-run
```

#### Skip Confirmation

Skip the confirmation prompt (use with caution):

```bash
hcp-node-autoscaling rollback \
  --hosted-cluster-id cluster-123 \
  --reason "Rolling back hosted cluster autoscaling migration" \
  --skip-confirmation
```

## Fleet Mode Features

Fleet mode provides a comprehensive view of autoscaling status across all management clusters in the fleet.

### How Fleet Mode Works

1. **Queries Fleet API**: Uses `/api/osd_fleet_mgmt/v1/management_clusters` to retrieve all management clusters
2. **Concurrent Auditing**: Audits all management clusters in parallel using goroutines for performance
3. **Flattened Output**: Outputs a single table with one row per hosted cluster across all MCs
4. **Progress Tracking**: Shows real-time progress as each management cluster is audited
5. **Error Handling**: Continues auditing even if individual management clusters fail

### Fleet Output Columns

The fleet audit table includes the following columns:

| Column | Description |
|--------|-------------|
| **MGMT CLUSTER NAME** | The management cluster name hosting this cluster |
| **CLUSTER ID** | The hosted cluster's internal ID |
| **CLUSTER NAME** | The hosted cluster's display name |
| **AUTOSCALING** | Whether autoscaling is enabled (Yes/No) |
| **HAS OVERRIDE** | Whether cluster-size-override annotation exists (Yes/No) |
| **CURRENT SIZE** | Current cluster size from label |
| **RECOMMENDED SIZE** | Recommended size from `hypershift.openshift.io/recommended-cluster-size` annotation (or "N/A") |

### Fleet Mode vs Single Cluster Mode

| Feature | Single Cluster Mode | Fleet Mode |
|---------|---------------------|------------|
| **Scope** | One management cluster | All management clusters |
| **Output** | Categorized (Group A, B, Already Configured) | Flattened table |
| **Filtering** | Supports --show-only | Supports --show-only |
| **Use Case** | Detailed analysis of one MC | Overview across entire fleet |
| **Performance** | Fast (single cluster) | Concurrent (multiple clusters) |

## Cluster Categories

The tool categorizes hosted clusters into three groups (single cluster mode only):

**Note:** Clusters with the `cluster-size-override` annotation will appear in MULTIPLE groups for visibility:
- Clusters with override + autoscaling enabled → appear in "Group A" AND "Already Configured"
- Clusters with override but no autoscaling → appear in "Group A" AND "Group B: Ready for Migration"

### Group A: Has Override Annotation

Clusters that have the `hypershift.openshift.io/cluster-size-override` annotation.

**Important:** Clusters in this group may also appear in "Already Configured" if they have autoscaling enabled.

The migrate command will:
- Add autoscaling to clusters that don't have it yet
- Skip clusters that already have autoscaling enabled

**Required Action**:
- For clusters without autoscaling: Run the migrate command to add autoscaling
- For clusters with autoscaling already enabled: Manually review and remove the `cluster-size-override` annotation
- Use the audit tool to identify which clusters appear in both groups

### Group B: Ready for Migration

Clusters that are missing the autoscaling annotation:
- Missing: `hypershift.openshift.io/resource-based-cp-auto-scaling: "true"`

**Important:** Clusters in this group may also appear in "Group A: Has Override Annotation" if they have the `cluster-size-override` annotation.

**Required Action**:
- Run the migrate command to automatically add the autoscaling annotation
- If cluster also appears in Group A: The override annotation will remain after migration - remove it manually

### Already Configured

Clusters that have the autoscaling annotation:
- Have: `hypershift.openshift.io/resource-based-cp-auto-scaling: "true"`

**Important:** Clusters in this group may also appear in "Group A: Has Override Annotation" if they have the `cluster-size-override` annotation that needs to be removed.

**Required Action**:
- If cluster appears ONLY in this group: None - autoscaling is fully configured
- If cluster also appears in Group A: Manually remove the `cluster-size-override` annotation

## How Migration Works

**Note:** The migrate command now includes clusters with `cluster-size-override` annotations.

The migrate command:

1. **Audits** the management cluster to find clusters ready for migration
2. **Displays** the list of candidates and asks for confirmation
3. **Patches** ManifestWork resources on the service cluster with the required annotations
4. **Verifies** the annotations are synced to the management cluster (polls every 15 seconds, 5-minute timeout)
5. **Reports** migration results including any errors

The migrate command uses elevated permissions (cluster-admin via backplane) to patch ManifestWork resources on the service cluster.

## How Rollback Works

The rollback command:

1. **Queries** OCM to find the management and service clusters for the hosted cluster
2. **Connects** to the management cluster to locate the hosted cluster
3. **Verifies** the cluster currently has autoscaling enabled
4. **Displays** rollback information and asks for confirmation
5. **Patches** ManifestWork resources on the service cluster to remove the autoscaling annotation
6. **Verifies** the annotation removal is synced to the management cluster (polls every 15 seconds, 5-minute timeout)
7. **Reports** rollback result

The rollback command uses elevated permissions (cluster-admin via backplane) to patch ManifestWork resources on the service cluster.

## Environment Support

The tool supports both production and staging environments:
- **Production**: Scans namespaces matching `ocm-production-${CLUSTER_ID}`
- **Staging**: Scans namespaces matching `ocm-staging-${CLUSTER_ID}`

Both environments are scanned automatically without additional configuration.

## Authentication

The tool uses the OCM SDK for authentication. Ensure you have:
1. Valid OCM credentials configured (via `ocm login`)
2. Access to the management cluster via backplane
3. Access to the service cluster via backplane (for migrate command)

## Example Output

### Audit - Single Cluster Text Format

```
Auditing management cluster: mgmt-cluster-prod (abc123def456)
Found 150 OCM namespaces to audit (production and staging)

Management Cluster: abc123def456
Total Hosted Clusters Scanned: 150

=== GROUP A: Has Override Annotation (5 clusters) ===
Clusters with cluster-size-override annotation (may also have autoscaling enabled):

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-001          prod-app-01          ocm-production-cluster-001   m54xl
cluster-002          staging-app-02       ocm-staging-cluster-002      m52xl
...

=== GROUP B: Ready for Migration (120 clusters) ===
Clusters ready for autoscaling migration (may also have cluster-size-override to remove):

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-003          prod-api-01          ocm-production-cluster-003   m52xl
cluster-004          staging-web-01       ocm-staging-cluster-004      m5xl
...

=== Already Configured (25 clusters) ===
Clusters with autoscaling enabled (may also have cluster-size-override to remove):

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-005          prod-db-01           ocm-production-cluster-005   m54xl
cluster-006          staging-cache-01     ocm-staging-cluster-006      m52xl
...

Summary:
  - Group A (Has Override Annotation): 5 clusters
  - Group B (Ready for migration): 120 clusters
  - Already configured: 25 clusters
  - Errors: 0 namespaces
```

### Audit - Fleet Mode Text Format

```
Fleet-wide audit mode: Fetching management clusters...
Found 5 management clusters in fleet
Auditing clusters concurrently...

Progress: 1/5 management clusters audited
Progress: 2/5 management clusters audited
Progress: 3/5 management clusters audited
Progress: 4/5 management clusters audited
Progress: 5/5 management clusters audited

=== Fleet-Wide Audit Results ===
Timestamp: 2024-01-15T10:30:00Z
Total Management Clusters: 5
Total Hosted Clusters: 450

MGMT CLUSTER NAME    CLUSTER ID           CLUSTER NAME         AUTOSCALING   HAS OVERRIDE   CURRENT SIZE   RECOMMENDED SIZE
mgmt-001             cluster-001          prod-app-01          Yes           Yes            m54xl          m52xl
mgmt-001             cluster-003          prod-api-01          No            No             m52xl          N/A
mgmt-001             cluster-005          prod-db-01           Yes           No             m54xl          m54xl
mgmt-002             cluster-010          staging-web-01       No            No             m5xl           N/A
mgmt-002             cluster-012          staging-api-02       Yes           No             m52xl          m52xl
...
```

### Migrate - Text Format

```
Service Cluster: svc-cluster-01 (svc-123)
Management Cluster: mgmt-cluster-prod (mgmt-456)
ManifestWork Namespace: mgmt-cluster-prod

Scanning 150 namespaces for migration candidates...

=== Clusters Ready for Migration (3) ===

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-003          prod-api-01          ocm-production-cluster-003   m52xl
cluster-007          prod-web-02          ocm-production-cluster-007   m5xl
cluster-008          staging-api-01       ocm-staging-cluster-008      m54xl

These clusters will receive the following annotation:
  - hypershift.openshift.io/resource-based-cp-auto-scaling: "true"

Do you want to proceed? [y/N]: y

[1/3] Migrating cluster prod-api-01 (cluster-003)...
  - Patched ManifestWork on service cluster
  - Waiting for sync (timeout: 5 minutes)...
  - Attempt 1: Annotations not yet synced
  - Verified: Annotations synced to management cluster
✓ Successfully migrated cluster-003

[2/3] Migrating cluster prod-web-02 (cluster-007)...
  - Patched ManifestWork on service cluster
  - Waiting for sync (timeout: 5 minutes)...
  - Verified: Annotations synced to management cluster
✓ Successfully migrated cluster-007

[3/3] Migrating cluster staging-api-01 (cluster-008)...
  - Patched ManifestWork on service cluster
  - Waiting for sync (timeout: 5 minutes)...
  - Verified: Annotations synced to management cluster
✓ Successfully migrated cluster-008


=== Migration Summary ===

Total candidates: 3
Successfully migrated: 3
Failed: 0

✓ Successfully Migrated:
  - prod-api-01 (cluster-003)
  - prod-web-02 (cluster-007)
  - staging-api-01 (cluster-008)
```

### Audit - Single Cluster JSON Format

```json
{
  "mgmt_cluster_id": "abc123def456",
  "total_scanned": 150,
  "needs_label_removal": [
    {
      "cluster_id": "cluster-001",
      "cluster_name": "prod-app-01",
      "namespace": "ocm-production-cluster-001",
      "current_size": "m54xl",
      "category": "needs-removal",
      "annotations": {
        "hypershift.openshift.io/cluster-size-override": "m54xl"
      }
    }
  ],
  "ready_for_migration": [...],
  "already_configured": [...],
  "errors": []
}
```

### Audit - Fleet Mode JSON Format

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "total_mgmt_clusters": 5,
  "total_hosted_clusters": 450,
  "clusters": [
    {
      "mgmt_cluster_id": "mgmt-001",
      "cluster_id": "cluster-001",
      "cluster_name": "prod-app-01",
      "autoscaling_enabled": false,
      "has_override": true,
      "current_size": "m54xl",
      "recommended_size": "large"
    },
    {
      "mgmt_cluster_id": "mgmt-001",
      "cluster_id": "cluster-003",
      "cluster_name": "prod-api-01",
      "autoscaling_enabled": false,
      "has_override": false,
      "current_size": "m52xl",
      "recommended_size": "medium"
    },
    {
      "mgmt_cluster_id": "mgmt-001",
      "cluster_id": "cluster-005",
      "cluster_name": "prod-db-01",
      "autoscaling_enabled": true,
      "has_override": false,
      "current_size": "large",
      "recommended_size": "N/A"
    }
  ],
  "errors": []
}
```

### Audit - Fleet Mode CSV Format

```csv
mgmt_cluster_name,cluster_id,cluster_name,autoscaling_enabled,has_override,current_size,recommended_size
mgmt-001,cluster-001,prod-app-01,true,true,m54xl,m52xl
mgmt-001,cluster-003,prod-api-01,false,false,m52xl,N/A
mgmt-001,cluster-005,prod-db-01,true,false,m52xl,m52xl
mgmt-002,cluster-010,staging-web-01,false,false,m5xl,N/A
mgmt-002,cluster-012,staging-api-02,true,false,m5xl,m5xl
```

## Flags Reference

### Audit Command

| Flag | Description | Default | Required |
|------|-------------|---------|----------|
| `--mgmt-cluster-id` | Management cluster ID/name to audit (mutually exclusive with --fleet) | - | One of --mgmt-cluster-id or --fleet |
| `--fleet` | Audit all management clusters in the fleet (mutually exclusive with --mgmt-cluster-id) | false | One of --mgmt-cluster-id or --fleet |
| `--output` | Output format: text, json, yaml, csv | text | No |
| `--show-only` | Filter: needs-removal, ready-for-migration, safe-to-remove-override | - | No |
| `--no-headers` | Skip headers in text/csv output | false | No |
| `-h, --help` | Show help message | - | No |

### Migrate Command

| Flag | Description | Default | Required |
|------|-------------|---------|----------|
| `--service-cluster-id` | Service cluster ID/name where ManifestWork resources exist | - | Yes |
| `--mgmt-cluster-id` | Management cluster ID/name to migrate | - | Yes |
| `--reason` | Reason for elevation (e.g., 'Enabling request serving node autoscaling') | - | Yes |
| `--dry-run` | Preview changes without applying them | false | No |
| `--skip-confirmation` | Skip confirmation prompt | false | No |
| `-h, --help` | Show help message | - | No |

### Rollback Command

| Flag | Description | Default | Required |
|------|-------------|---------|----------|
| `--hosted-cluster-id` | Hosted cluster ID/name/external-id to rollback | - | Yes |
| `--reason` | Reason for elevation (e.g., 'Rolling back hosted cluster autoscaling migration') | - | Yes |
| `--dry-run` | Preview changes without applying them | false | No |
| `--skip-confirmation` | Skip confirmation prompt | false | No |
| `-h, --help` | Show help message | - | No |

## Cluster Identifier Flexibility

Both `--mgmt-cluster-id` and `--service-cluster-id` flags accept:
- Cluster name (display name)
- Internal cluster ID
- External cluster ID

The tool automatically resolves the identifier via OCM.

## Error Handling

The tool uses graceful degradation:
- If a namespace fails to audit, the error is recorded and the tool continues
- If a cluster migration fails, other clusters continue to be migrated
- All errors are reported in the output
- Non-fatal errors: Missing HostedClusters, annotation read errors, sync timeouts
- Fatal errors: K8s client creation, OCM connection, invalid cluster identifiers

## Operations

### Audit Command
Performs **read-only** operations:
- Lists namespaces
- Reads HostedCluster resources
- Reads annotations and labels
- Does NOT modify any cluster resources

**Fleet Mode Additional Operations:**
- Queries fleet management API to retrieve all management cluster IDs
- Connects to multiple management clusters concurrently
- Aggregates results across all clusters

Uses non-elevated permissions.

### Migrate Command
Performs **write operations**:
- Reads ManifestWork resources from service cluster
- Updates ManifestWork resources with autoscaling annotations
- Polls HostedCluster resources on management cluster to verify sync

Uses elevated permissions (cluster-admin via backplane) with audit trail. Requires `--reason` flag for audit purposes.

### Rollback Command
Performs **write operations**:
- Queries OCM to find management and service clusters using `utils.GetManagementCluster` and `utils.GetServiceCluster`
- Connects to management cluster to locate HostedCluster
- Reads ManifestWork resources from service cluster
- Updates ManifestWork resources to remove autoscaling annotations
- Polls HostedCluster resources on management cluster to verify sync

Uses elevated permissions (cluster-admin via backplane) with audit trail. Requires `--reason` flag for audit purposes.

## Dependencies

- OCM SDK (`github.com/openshift-online/ocm-sdk-go`)
- osdctl utilities (`github.com/openshift/osdctl/pkg`)
- HyperShift API (`github.com/openshift/hypershift/api`)
- Open Cluster Management API (`open-cluster-management.io/api/work/v1`)
- Kubernetes client libraries
- Cobra CLI framework

## Contributing

This tool is part of the ROSA HCP Platform Tools repository. For issues or feature requests, please open an issue in the repository.

## License

See the LICENSE file in the root of the rosa-hcp-platform-tools repository.
