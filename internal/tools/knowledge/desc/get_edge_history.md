Read immutable snapshots from a knowledge edge's history, newest first.

Identify the edge by kind and nonzero UUID. Set before to a version number to return only older versions; zero starts at the newest version. Limit controls the page size, with zero using the service default. Before and limit must be nonnegative.

The result contains a versions array with data, metadata and timestamps. A nonzero next_before value is the cursor for the next older page.
