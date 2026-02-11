#!/bin/bash

# Check if reason is provided
if [ -z "$1" ]; then
    echo "Usage: $0 <reason-for-elevation>"
    echo "Example: $0 'Retrieving hypershift credentials'"
    exit 1
fi

REASON="$1"

# Get list of management clusters
echo "Fetching management clusters..."
CLUSTERS=$(ocm get /api/osd_fleet_mgmt/v1/management_clusters | jq -r '.items[] | .name')

if [ -z "$CLUSTERS" ]; then
    echo "No management clusters found"
    exit 1
fi

# Count clusters
TOTAL=$(echo "$CLUSTERS" | wc -l | tr -d ' ')
echo "Found $TOTAL management clusters"
echo ""

# Loop over each cluster
COUNT=0
for MC in $CLUSTERS; do
    COUNT=$((COUNT + 1))
    echo "[$COUNT/$TOTAL] Processing cluster: $MC"
    echo "========================================================================"

    # Login to the cluster
    echo "  Logging in to $MC"
    ocm-backplane login "$MC"

    # Elevate and run commands
    echo "  Running elevated commands with reason ${REASON}"

    ### START CUSTOMIZABLE SECTION ###

    ### END CUSTOMIZABLE SECTION ###

    echo ""
done

echo "Processing complete!"
