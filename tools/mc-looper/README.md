# MC Looper

A simple bash script to loop over all management clusters and execute commands.

## Usage

```bash
./mc-looper.sh "reason for elevation"
```

Example:
```bash
./mc-looper.sh "SREP-1234"
```

## Customization

Edit the section between `### START CUSTOMIZABLE SECTION ###` and `### END CUSTOMIZABLE SECTION ###` in the script to change what commands are executed on each management cluster.

The current example retrieves and decodes the hypershift-operator-private-link-credentials secret.

## Prerequisites

- `ocm` CLI tool
- `ocm-backplane` CLI tool
- `jq` for JSON processing
