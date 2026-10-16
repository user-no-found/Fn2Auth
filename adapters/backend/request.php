<?php
// The only dynamic entry point is reached through a private per-user gateway channel.
$secret = getenv('FN2AUTH_INSTANCE_SECRET');
if (!is_string($secret) || strlen($secret) < 32 ||
    !hash_equals($secret, (string) ($_SERVER['HTTP_X_FN2AUTH_INSTANCE'] ?? ''))) {
    http_response_code(403);
    exit;
}
$prefix = getenv('FN2AUTH_PREFIX');
$_SERVER['REMOTE_ADDR'] = '127.0.0.1';
$_SERVER['SCRIPT_NAME'] = $prefix . '/index.php';
$_SERVER['PHP_SELF'] = $_SERVER['SCRIPT_NAME'];
$_SERVER['HTTP_REMOTE_USER'] = getenv('FN2AUTH_USERNAME');
// Neither a browser nor a NAS-admin role may select a different upstream user.
unset($_SERVER['HTTP_AUTHORIZATION'], $_SERVER['HTTP_X_TRIM_ISADMIN']);
foreach (array_keys($_SERVER) as $key) {
    if (str_starts_with($key, 'HTTP_X_FORWARDED_') || $key === 'HTTP_FORWARDED') {
        unset($_SERVER[$key]);
    }
}
if (parse_url($_SERVER['REQUEST_URI'] ?? '', PHP_URL_PATH) === $prefix . '/_instance/ready') {
    header('Content-Type: text/plain');
    echo getenv('FN2AUTH_READY_ID');
    exit;
}
// Serialize this user's dynamic requests with import commits and HOTP generation.
// Other business users have independent locks and PHP instances.
$mutationLock = fopen(getenv('FN2AUTH_STATE') . '/mutations.lock', 'c');
if (!$mutationLock || !flock($mutationLock, LOCK_EX)) {
    http_response_code(503);
    exit;
}
register_shutdown_function(static function () use ($mutationLock) {
    flock($mutationLock, LOCK_UN);
    fclose($mutationLock);
});
