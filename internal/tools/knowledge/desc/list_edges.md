List current knowledge edges in deterministic creation order.

Optionally restrict results to one edge kind. You may also provide either source_id or target_id as a nonzero node UUID to filter by one endpoint; providing both selectors is invalid. Omit both selectors to list without an endpoint filter. Limit defaults to the service default and is bounded by the service.

The result contains an edges array with current endpoint, data and revision fields.
