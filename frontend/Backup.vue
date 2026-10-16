<script setup>
import { ref, onBeforeUnmount, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useTwofaccounts } from '@/stores/twofaccounts'
import { exportBackup, previewBackup, importBackup, backupError } from './backup-api'

const router = useRouter()
const accounts = useTwofaccounts()
const passphrase = ref('')
const file = ref(null)
const preview = ref(null)
const result = ref(null)
const error = ref('')
const busy = ref(false)
const exported = ref(false)
let generation = 0
let controller = null

function cancel() {
    generation++
    controller?.abort(); controller = null
    passphrase.value = ''
    preview.value = null
    busy.value = false
}
function choose(event) {
    cancel(); error.value = ''; result.value = null
    file.value = event.target.files?.[0] || null
    event.target.value = ''
}
async function perform(operation) {
    if (busy.value) return
    const length = Array.from(passphrase.value).length
    if (operation !== 'import' && (length < 10 || length > 1024)) {
        error.value = 'fn2auth.backup.passphraseInvalid'; return
    }
    const token = preview.value?.preview_token
    if (operation === 'import' && !token) return
    const attempt = ++generation
    const active = controller = new AbortController()
    const password = passphrase.value
    busy.value = true; error.value = ''; result.value = null; exported.value = false
    preview.value = null
    try {
        if (operation === 'export') {
            await exportBackup(password, active.signal)
            if (attempt === generation) exported.value = true
        } else if (operation === 'preview') {
            const data = await previewBackup(file.value, password, active.signal)
            if (attempt === generation) preview.value = data
        } else {
            const data = await importBackup(token, active.signal)
            if (attempt === generation) {
                result.value = data
                accounts.fetchedOn = null
            }
        }
    } catch (reason) {
        if (attempt === generation && reason.code !== 'ERR_CANCELED') {
            const key = await backupError(reason)
            if (attempt === generation) error.value = key
        }
    } finally {
        if (attempt === generation) { busy.value = false; controller = null; passphrase.value = '' }
    }
}
function visibility() { if (document.hidden) cancel() }
onMounted(() => {
    window.addEventListener('pagehide', cancel)
    document.addEventListener('visibilitychange', visibility)
})
onBeforeUnmount(() => {
    cancel()
    file.value = null
    window.removeEventListener('pagehide', cancel)
    document.removeEventListener('visibilitychange', visibility)
})
</script>

<template>
    <StackLayout>
        <template #content>
            <section class="fn2auth-backup" data-testid="backup-page">
                <h1 class="title is-4">{{ $t('fn2auth.backup.title') }}</h1>
                <p class="mb-4">{{ $t('fn2auth.backup.description') }}</p>
                <label class="label" for="backup-password">{{ $t('fn2auth.backup.passphrase') }}</label>
                <input id="backup-password" v-model="passphrase" class="input" type="password" autocomplete="off" maxlength="2048" :disabled="busy" data-testid="backup-passphrase">
                <p class="help mb-4">{{ $t('fn2auth.backup.passphraseHelp') }}</p>
                <div class="buttons">
                    <button type="button" class="button is-link" :disabled="busy" @click="perform('export')" data-testid="backup-export">{{ $t('fn2auth.backup.export') }}</button>
                    <label class="button fn2auth-file">
                        {{ $t('fn2auth.backup.file') }}
                        <input type="file" accept=".age,application/octet-stream" :disabled="busy" @change="choose" data-testid="backup-file" :aria-label="$t('fn2auth.backup.file')">
                    </label>
                    <button type="button" class="button" :disabled="busy || !file" @click="perform('preview')" data-testid="backup-preview">{{ $t('fn2auth.backup.preview') }}</button>
                </div>
                <p v-if="file" class="mb-3">{{ file.name }}</p>
                <p v-if="busy" role="status">{{ $t('fn2auth.backup.working') }}</p>
                <p v-if="error" role="alert" class="notification is-warning" data-testid="backup-error">{{ $t(error) }}</p>
                <p v-if="exported" role="status">{{ $t('fn2auth.backup.exported') }}</p>
                <p v-if="result" role="status" data-testid="backup-result">{{ $t('fn2auth.backup.completed', { imported: result.imported, skipped: result.skipped, updated: result.updated_counters }) }}</p>
                <div v-if="preview" data-testid="backup-confirmation">
                    <p class="mb-3">{{ $t('fn2auth.backup.expires') }}</p>
                    <ul class="mb-4">
                        <li v-for="(account, index) in preview.accounts" :key="index" class="box">
                            <strong>{{ account.service }}</strong> · {{ account.account }} · {{ account.otp_type }}
                            <p>{{ $t(account.exists ? 'fn2auth.backup.existing' : 'fn2auth.backup.new') }}</p>
                            <p v-if="account.otp_type === 'hotp' && account.counter !== null && account.counter !== undefined">{{ $t('fn2auth.backup.counter', { counter: account.counter }) }}</p>
                        </li>
                    </ul>
                    <button type="button" class="button is-link" :disabled="busy" @click="perform('import')" data-testid="backup-confirm">{{ $t('fn2auth.backup.confirm') }}</button>
                </div>
                <button v-if="busy || preview" type="button" class="button mt-3" @click="cancel" data-testid="backup-cancel">{{ $t('fn2auth.backup.cancel') }}</button>
            </section>
        </template>
        <template #footer><VueFooter><NavigationButton action="back" @goback="router.push({name:'accounts'})" /></VueFooter></template>
    </StackLayout>
</template>

<style scoped>
.fn2auth-backup { max-width: 48rem; margin: auto; padding: 1rem; overflow-wrap: anywhere; }
.fn2auth-backup .button { min-height: 44px; height: auto; white-space: normal; }
.fn2auth-file { position: relative; overflow: hidden; }
.fn2auth-file input { position: absolute; inset: 0; height: 100%; width: 100%; opacity: 0; cursor: pointer; }
.fn2auth-file:focus-within { outline: 2px solid currentColor; outline-offset: 2px; }
</style>
