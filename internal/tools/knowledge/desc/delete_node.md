Delete a knowledge node by kind and nonzero UUID.

Provide expected_revision to require that the current revision still matches before deletion. The service may reject deletion while edges reference the node. Success returns an empty result object.
