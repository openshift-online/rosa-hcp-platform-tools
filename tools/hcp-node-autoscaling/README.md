# HCP Node Autoscaling Tool

A tool to audit and migrate hosted clusters to resource-based node autoscaling.

## Overview

This tool provides two subcommands:

1. **audit**: Analyzes hosted clusters and categorizes them based on autoscaling migration readiness
2. **migrate**: Patches ManifestWork resources to enable resource-based node autoscaling

The tool inspects cluster annotations and can automatically migrate clusters that are ready for autoscaling.

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

The audit command analyzes clusters and reports their autoscaling readiness.

#### Basic Audit

```bash
hcp-node-autoscaling audit --mgmt-cluster-id <MANAGEMENT_CLUSTER_ID>
```

#### Output Formats

##### Text (Default)
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123
```

##### JSON
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --output json
```

#### Filtering Results

##### Show only clusters that need annotation removal
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --show-only needs-removal
```

##### Show only clusters ready for migration
```bash
hcp-node-autoscaling audit --mgmt-cluster-id mgmt-123 --show-only ready-for-migration
```

### Migrate Command

The migrate command automatically patches clusters that are ready for autoscaling migration.

#### Basic Migration

```bash
hcp-node-autoscaling migrate \
  --service-cluster-id <SERVICE_CLUSTER_ID> \
  --mgmt-cluster-id <MANAGEMENT_CLUSTER_ID>
```

#### Dry Run

Preview what would be migrated without making changes:

```bash
hcp-node-autoscaling migrate \
  --service-cluster-id svc-123 \
  --mgmt-cluster-id mgmt-456 \
  --dry-run
```

#### Skip Confirmation

Skip the confirmation prompt (use with caution):

```bash
hcp-node-autoscaling migrate \
  --service-cluster-id svc-123 \
  --mgmt-cluster-id mgmt-456 \
  --skip-confirmation
```

## Cluster Categories

The tool categorizes hosted clusters into three groups:

### Group A: Needs Annotation Removal

Clusters that have the `hypershift.openshift.io/cluster-size-override` annotation.

**Required Action**: Remove the `cluster-size-override` annotation before enabling autoscaling.

### Group B: Ready for Migration

Clusters that meet ALL conditions:
- Do NOT have `hypershift.openshift.io/cluster-size-override` annotation
- Missing the required autoscaling annotation:
  - `hypershift.openshift.io/resource-based-cp-auto-scaling: "true"`

**Required Action**: Run the migrate command to automatically add the required annotation.

### Already Configured

Clusters that have the required autoscaling annotation properly set.

**Required Action**: None - autoscaling is already configured.

## How Migration Works

The migrate command:

1. **Audits** the management cluster to find clusters ready for migration
2. **Displays** the list of candidates and asks for confirmation
3. **Patches** ManifestWork resources on the service cluster with the required annotations
4. **Verifies** the annotations are synced to the management cluster (polls every 15 seconds, 5-minute timeout)
5. **Reports** migration results including any errors

The migrate command uses elevated permissions (cluster-admin via backplane) to patch ManifestWork resources on the service cluster.

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

### Audit - Text Format

```
Auditing management cluster: mgmt-cluster-prod (abc123def456)
Found 150 OCM namespaces to audit (production and staging)

Management Cluster: abc123def456
Total Hosted Clusters Scanned: 150

=== GROUP A: Needs Annotation Removal (5 clusters) ===
These clusters have the cluster-size-override annotation that must be removed:

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-001          prod-app-01          ocm-production-cluster-001   m54xl
cluster-002          staging-app-02       ocm-staging-cluster-002      m52xl
...

=== GROUP B: Ready for Migration (120 clusters) ===
These clusters can be immediately migrated to autoscaling:

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-003          prod-api-01          ocm-production-cluster-003   m52xl
cluster-004          staging-web-01       ocm-staging-cluster-004      m5xl
...

=== Already Configured (25 clusters) ===
These clusters already have autoscaling annotations set:

CLUSTER ID           CLUSTER NAME         NAMESPACE                    CURRENT SIZE
cluster-005          prod-db-01           ocm-production-cluster-005   large
cluster-006          staging-cache-01     ocm-staging-cluster-006      m52xl
...

Summary:
  - Group A (Needs annotation removal): 5 clusters
  - Group B (Ready for migration): 120 clusters
  - Already configured: 25 clusters
  - Errors: 0 namespaces
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

### Audit - JSON Format

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

## Flags Reference

### Audit Command

| Flag | Description | Default | Required |
|------|-------------|---------|----------|
| `--mgmt-cluster-id` | Management cluster ID/name to audit | - | Yes |
| `--output` | Output format: text, json, yaml, csv | text | No |
| `--show-only` | Filter: needs-removal, ready-for-migration | - | No |
| `--no-headers` | Skip headers in text/csv output | false | No |
| `-h, --help` | Show help message | - | No |

### Migrate Command

| Flag | Description | Default | Required |
|------|-------------|---------|----------|
| `--service-cluster-id` | Service cluster ID/name where ManifestWork resources exist | - | Yes |
| `--mgmt-cluster-id` | Management cluster ID/name to migrate | - | Yes |
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

Uses non-elevated permissions.

### Migrate Command
Performs **write operations**:
- Reads ManifestWork resources from service cluster
- Updates ManifestWork resources with autoscaling annotations
- Polls HostedCluster resources on management cluster to verify sync

Uses elevated permissions (cluster-admin via backplane) with audit trail:
- Elevation reason: [SREP-2821](https://issues.redhat.com/browse/SREP-2821) Migrating hosted clusters to node autoscaling

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
