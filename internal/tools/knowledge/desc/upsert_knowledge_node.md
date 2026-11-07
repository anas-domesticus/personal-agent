Create or update a predefined knowledge node and queue its title and description embeddings.

Provide one of the predefined kinds: objective, workflow, claim, source or agent. Data must be a JSON object containing title, description and the other fields required by that kind.

To create a node, omit id and expected_revision; the service assigns its id. To update a node, provide its UUID in id. Provide expected_revision with the current revision to protect against concurrent updates. A revision mismatch or another conflicting write returns an error instead of overwriting the newer data. Omit expected_revision only when an unconditional update is intended.

The result is the current node, including its id, kind, revision, data and timestamps.
