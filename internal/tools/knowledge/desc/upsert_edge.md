Create or update the unique knowledge edge of a kind between two node references.

Source and target are nested objects, each containing the endpoint node's kind and nonzero UUID. The selected edge type must permit that source-kind to target-kind combination. Data must match the edge kind's schema; optional metadata is stored with the revision in history.

The edge is identified by its kind and endpoints. Provide expected_revision with the current revision for an optimistic-concurrency update; omit it when creating an edge or when an unconditional update is intended. A revision mismatch returns an error instead of overwriting newer data.

The result is the current edge, including its id, endpoints, revision, data and timestamps.
