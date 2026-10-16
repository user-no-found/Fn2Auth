<?php
// The service selects both paths; this CLI entry never reads browser parameters.
$database = getenv('DB_DATABASE');
$destination = getenv('FN2AUTH_SNAPSHOT');
$parent = getenv('FN2AUTH_STATE') . '/snapshots/';
if (!$database || !$destination || !str_starts_with($destination, $parent) || file_exists($destination)) {
    fwrite(STDERR, "invalid_snapshot_target\n");
    exit(1);
}
$connection = new PDO('sqlite:' . $database);
$connection->exec('VACUUM INTO ' . $connection->quote($destination));
