<?php
// Every mutable Laravel path belongs to the selected business user's instance.
$state = getenv('FN2AUTH_STATE');
if (!$state || !is_dir($state) || !is_file($state . '/app.key')) {
    throw new RuntimeException('instance_state_unavailable');
}
$app->useStoragePath($state . '/storage');
$app->useEnvironmentPath($state);
// Credentials are passed by the application service; there is no shared .env.
$app->booted(function () use ($app) {
    $app['url']->forceRootUrl(rtrim((string) config('app.url'), '/'));
});
