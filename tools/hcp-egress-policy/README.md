# hcp-egress-policy

A tool for setting the egress policy label on ROSA HCP hosted clusters by patching their ManagedCluster resources.

## Overview

This tool sets the `api.openshift.com/hosted-cluster-egress-policy: NoEgress` label on a hosted cluster by:

1. Looking up the service cluster from OCM using the hosted cluster ID
2. Connecting to the service cluster with elevated permissions via backplane-api
3. Patching the ManagedCluster resource with the egress policy label

## Prerequisites

- OCM authentication configured (via `ocm login`)
- Access to the hosted cluster and service cluster
- Backplane-API access for elevated permissions

## Installation

```bash
cd tools/hcp-egress-policy
go build -o hcp-egress-policy main.go
```

## Usage

### Single Cluster Mode

Set egress policy for a specific hosted cluster:

```bash
./hcp-egress-policy --cluster-id <cluster-id> --elevation-reason <jira-url>
```

### All Clusters Mode

Set egress policy for all clusters with the `zero_egress='true'` property:

```bash
./hcp-egress-policy --all --elevation-reason <jira-url>
```

### Dry Run

Preview what would be changed without applying:

```bash
./hcp-egress-policy --cluster-id <cluster-id> --elevation-reason <jira-url> --dry-run
```

### Skip Confirmation

Skip the confirmation prompt (use with caution):

```bash
./hcp-egress-policy --all --elevation-reason <jira-url> --skip-confirmation
```

## Flags

- `--cluster-id`: The hosted cluster ID, name, or external ID (mutually exclusive with `--all`)
- `--all`: Process all clusters with `zero_egress='true'` property (mutually exclusive with `--cluster-id`)
- `--elevation-reason` (required): Reason for elevation (Jira ticket URL)
- `--dry-run`: Preview changes without applying them
- `--skip-confirmation`: Skip the confirmation prompt

**Note:** You must specify either `--cluster-id` or `--all`, but not both.

## Examples

```bash
# Set egress policy for a specific cluster
./hcp-egress-policy --cluster-id 1a2b3c4d5e6f7g8h9i0j --elevation-reason https://issues.redhat.com/browse/SREP-3303

# Set egress policy for all clusters with zero_egress='true'
./hcp-egress-policy --all --elevation-reason https://issues.redhat.com/browse/SREP-3303

# Dry run for all clusters to see what would change
./hcp-egress-policy --all --elevation-reason https://issues.redhat.com/browse/SREP-3303 --dry-run

# Process all clusters without confirmation (useful for automation)
./hcp-egress-policy --all --elevation-reason https://issues.redhat.com/browse/SREP-3303 --skip-confirmation
```

## How It Works

### Single Cluster Mode
1. **Cluster Lookup**: Uses OCM API to resolve the cluster ID and find its service cluster
2. **Client Creation**: Creates a Kubernetes client for the service cluster with elevated access via backplane-api
3. **ManagedCluster Patch**: Adds the `api.openshift.com/hosted-cluster-egress-policy: NoEgress` label to the ManagedCluster resource

### All Clusters Mode
1. **Cluster Discovery**: Queries OCM API for all clusters with `properties.zero_egress='true'`
2. **Batch Processing**: For each cluster found:
   - Looks up the service cluster
   - Creates a Kubernetes client with elevated permissions
   - Patches the ManagedCluster resource
3. **Summary Report**: Displays success/failure count and details for all processed clusters

## Notes

- The tool uses elevated permissions on the service cluster via backplane-api
- The elevation reason (Jira ticket URL) is required and logged for audit purposes
- Changes are made directly to the ManagedCluster resource on the service cluster
- If the label is already set to NoEgress, the tool will warn but still allow the operation
- When using `--all` mode, the tool continues processing remaining clusters even if some fail
