Create or update a knowledge node and queue its title and description embeddings.

Data must be a JSON object containing title, description and the other fields required by the selected kind. Optional metadata is stored with the revision in history.

To create a node, omit id and expected_revision; the service assigns its id. To update a node, provide its nonzero UUID in id. Provide expected_revision with the current revision to protect against concurrent updates. A revision mismatch returns an error instead of overwriting newer data. Omit expected_revision only when an unconditional update is intended.

The result is the current node, including its id, kind, revision, data and timestamps.
