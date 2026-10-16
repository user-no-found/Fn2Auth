<?php
// Private CLI protocol: OTP secrets use stdin/stdout only, never argv or diagnostic logs.
require __DIR__ . '/../vendor/autoload.php';
$app = require __DIR__ . '/app.php';
$kernel = $app->make(Illuminate\Contracts\Console\Kernel::class);
$kernel->bootstrap();

use App\Api\v1\Requests\TwoFAccountStoreRequest;
use App\Models\TwoFAccount;
use App\Models\User;
use Illuminate\Support\Facades\Auth;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Validator;

function identityKey(TwoFAccount $account): string
{
    return hash('sha256', json_encode([$account->otp_type, $account->account, $account->service,
        $account->secret, $account->algorithm, (int) $account->digits, $account->period]));
}

function prepareAccounts(array $document): array
{
    if (($document['schema'] ?? null) !== 'fn2auth-otp-v1' || !isset($document['accounts']) ||
        !is_array($document['accounts']) || count($document['accounts']) > 10000) {
        throw new RuntimeException('invalid_backup_schema');
    }
    $result = [];
    $allowed = ['otp_type', 'account', 'service', 'secret', 'digits', 'algorithm', 'period', 'counter'];
    foreach ($document['accounts'] as $row) {
        if (!is_array($row) || array_diff(array_keys($row), $allowed)) {
            throw new RuntimeException('invalid_account_fields');
        }
        $rules = (new TwoFAccountStoreRequest)->rules();
        $rules['secret'] = array_merge(['required'], $rules['secret']);
        $rules['account'] .= '|max:191';
        $rules['service'] .= '|max:191';
        $rules['counter'] .= '|max:9007199254740991';
        $rules['period'] .= '|max:86400';
        $validated = Validator::make($row, $rules)->validate();
        $account = new TwoFAccount;
        $account->fillWithOtpParameters($validated, true);
        $result[] = $account;
    }
    return $result;
}

try {
    $lock = fopen(getenv('FN2AUTH_STATE') . '/mutations.lock', 'c');
    if (!$lock || !flock($lock, LOCK_EX)) throw new RuntimeException('vault_busy');
    $user = User::where('name', getenv('FN2AUTH_USERNAME'))->sole();
    Auth::setUser($user);
    $action = $argv[1] ?? '';
    if ($action === 'export') {
        $rows = [];
        foreach ($user->twofaccounts()->limit(10001)->get() as $account) {
            $rows[] = ['otp_type' => $account->otp_type, 'account' => $account->account,
                'service' => $account->service, 'secret' => $account->secret,
                'digits' => (int) $account->digits, 'algorithm' => $account->algorithm,
                'period' => $account->period === null ? null : (int) $account->period,
                'counter' => $account->counter === null ? null : (int) $account->counter];
        }
        if (count($rows) > 10000) throw new RuntimeException('backup_account_limit');
        $result = ['schema' => 'fn2auth-otp-v1', 'accounts' => $rows];
    } elseif ($action === 'preview' || $action === 'import') {
        $raw = stream_get_contents(STDIN, 8 * 1024 * 1024 + 1);
        if (strlen($raw) > 8 * 1024 * 1024) throw new RuntimeException('backup_size_limit');
        $accounts = prepareAccounts(json_decode($raw, true, 32, JSON_THROW_ON_ERROR));
        $result = DB::transaction(function () use ($accounts, $user, $action) {
            $existing = [];
            foreach ($user->twofaccounts()->get() as $account) $existing[identityKey($account)] = $account;
            $summary = [];
            $counts = ['schema' => 1, 'imported' => 0, 'skipped' => 0, 'updated_counters' => 0];
            foreach ($accounts as $account) {
                $key = identityKey($account);
                $previous = $existing[$key] ?? null;
                if ($action === 'preview') {
                    $summary[] = ['service' => $account->service, 'account' => $account->account,
                        'otp_type' => $account->otp_type, 'exists' => $previous !== null,
                        'counter' => $account->counter];
                    continue;
                }
                if ($previous) {
                    if ($account->otp_type === TwoFAccount::HOTP && (int) $account->counter > (int) $previous->counter) {
                        $previous->counter = $account->counter;
                        $previous->save();
                        $counts['updated_counters']++;
                    } else $counts['skipped']++;
                } else {
                    $user->twofaccounts()->save($account);
                    $existing[$key] = $account;
                    $counts['imported']++;
                }
            }
            return $action === 'preview' ? ['accounts' => $summary] : $counts;
        });
    } else throw new RuntimeException('invalid_backup_action');
    echo json_encode($result, JSON_THROW_ON_ERROR);
    flock($lock, LOCK_UN);
    fclose($lock);
} catch (Throwable $error) {
    fwrite(STDERR, "backup_operation_failed\n");
    exit(1);
}
