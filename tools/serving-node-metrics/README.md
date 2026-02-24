# Serving Node Metrics

CLI tool for querying request-serving node metrics from Dynatrace for ROSA HCP clusters. Outputs CPU, memory, disk I/O, and network metrics in a tabular format.

## Prerequisites

- Go 1.21+
- `ocm` CLI logged in
- `vault` CLI installed
- Access to Vault with Dynatrace credentials

## Build

```bash
go build -o serving-node-metrics .
```

## Usage

```bash
# Single cluster, last 1 hour (default)
./serving-node-metrics CLUSTER_ID --vault-addr https://your-vault-server --vault-path path/to/secret

# Multiple clusters
./serving-node-metrics CLUSTER_ID_1 CLUSTER_ID_2 CLUSTER_ID_3 --vault-addr https://your-vault-server --vault-path path/to/secret

# Custom time window (last 24 hours)
./serving-node-metrics CLUSTER_ID --vault-addr https://your-vault-server --vault-path path/to/secret --hours 24

# Last 60 days
./serving-node-metrics CLUSTER_ID --vault-addr https://your-vault-server --vault-path path/to/secret --hours 1440

# Using VAULT_ADDR env var instead of flag
export VAULT_ADDR=https://your-vault-server
./serving-node-metrics CLUSTER_ID --vault-path path/to/secret

# Or run directly with go
go run . CLUSTER_ID --vault-addr https://your-vault-server --vault-path path/to/secret --hours 24
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--hours`, `-H` | `1` | Time window in hours to query |
| `--vault-addr` | (none) | Vault server address (required, or set `VAULT_ADDR` env var) |
| `--vault-path` | (none) | Vault secret path for Dynatrace credentials (required) |

## Output

Produces a table with per-node averages:

```
+----------------+-------------------+--------+----------+-------------+--------------+-----------+----------+
|   CLUSTER ID   |       NODE        | CPU %  | MEMORY % | DISK I/O IN | DISK I/O OUT |   NET IN  | NET OUT  |
+----------------+-------------------+--------+----------+-------------+--------------+-----------+----------+
| 2abc123def456  | ip-10-0-1-100     | 34.21% | 62.15%   | 0.001234    | 0.002345     | 0.003456  | 0.004567 |
| 2abc123def456  | ip-10-0-1-101     | 28.54% | 58.92%   | 0.001100    | 0.002100     | 0.003200  | 0.004300 |
+----------------+-------------------+--------+----------+-------------+--------------+-----------+----------+
```

## Metrics Collected

- **CPU %** - Average CPU consumption
- **Memory %** - Average memory consumption
- **Disk I/O In** - Average disk read
- **Disk I/O Out** - Average disk write
- **Net In** - Average network ingress
- **Net Out** - Average network egress

## How It Works

1. Authenticates with Vault to retrieve Dynatrace OAuth credentials
2. Exchanges credentials for a Dynatrace access token
3. Uses `ocm` to look up cluster external IDs
4. Queries Dynatrace DQL API for `hypershift_node_consumption_*` metrics
5. Calculates per-node averages over the specified time window
6. Displays results in a formatted table
