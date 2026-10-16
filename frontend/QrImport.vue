<script setup>
import { ref, onBeforeUnmount, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useBusStore } from '@/stores/bus'
import { QrReader } from './qr-reader'
import { previewCode } from './qr-workflow'
import { qrError } from './qr-errors'

const emit = defineEmits(['choosing'])
const router = useRouter()
const bus = useBusStore()
const busy = ref('')
const error = ref(null)
const pickerWaiting = ref(false)
let generation = 0
let timer = null
let controller = null
let reader = null

function cancel() {
    generation++
    clearTimeout(timer)
    pickerWaiting.value = false
    controller?.abort(); controller = null
    reader?.close(); reader = null
    busy.value = ''
}
function pickerDone() { clearTimeout(timer); pickerWaiting.value = false }
function choosing(photo) {
    emit('choosing')
    pickerDone()
    if (photo) timer = setTimeout(() => { pickerWaiting.value = true }, 10000)
}
async function read(file) {
    if (!file || busy.value) return
    emit('choosing')
    cancel()
    error.value = null
    const attempt = generation
    const active = controller = new AbortController()
    const decoder = reader = new QrReader()
    busy.value = 'decoding'
    try {
        if (file.size > 20 * 1024 * 1024) throw Object.assign(new Error('imageTooLarge'), { code: 'imageTooLarge' })
        const value = await decoder.read({ file }, { signal: active.signal, timeout: 20000 })
        decoder.close()
        if (attempt !== generation) return
        busy.value = 'validating'
        await previewCode(value, { signal: active.signal, bus, router })
    } catch (reason) {
        if (attempt === generation && reason.code !== 'ERR_CANCELED') error.value = qrError(reason)
    } finally {
        decoder.close()
        if (attempt === generation) { busy.value = ''; controller = null; reader = null }
    }
}
function changed(event) {
    pickerDone()
    const file = event.target.files?.[0]
    event.target.value = ''
    void read(file)
}
function visibility() { if (document.hidden) cancel() }
onMounted(() => {
    document.addEventListener('visibilitychange', visibility)
    window.addEventListener('pagehide', cancel)
})
onBeforeUnmount(() => {
    cancel()
    document.removeEventListener('visibilitychange', visibility)
    window.removeEventListener('pagehide', cancel)
})
defineExpose({ read, cancel })
</script>

<template>
    <section class="fn2auth-import" :aria-busy="Boolean(busy)">
        <div class="buttons is-centered">
            <label class="button is-rounded fn2auth-file" :aria-disabled="Boolean(busy)">
                {{ $t('fn2auth.camera.image') }}
                <input type="file" accept="image/*" :disabled="Boolean(busy)" data-testid="qr-image-input"
                    :aria-label="$t('fn2auth.camera.image')" @click="choosing(false)" @change="changed" @cancel="pickerDone">
            </label>
            <label class="button is-rounded fn2auth-file" :aria-disabled="Boolean(busy)">
                {{ $t('fn2auth.camera.photo') }}
                <input type="file" accept="image/*" capture="environment" :disabled="Boolean(busy)" data-testid="qr-photo-input"
                    :aria-label="$t('fn2auth.camera.photo')" @click="choosing(true)" @change="changed" @cancel="pickerDone">
            </label>
        </div>
        <p v-if="busy" role="status">{{ $t('fn2auth.camera.' + busy) }}</p>
        <button v-if="busy" type="button" class="button my-2" @click="cancel" data-testid="qr-cancel">{{ $t('fn2auth.cancel') }}</button>
        <p v-if="pickerWaiting" role="status" data-testid="qr-picker-message">{{ $t('fn2auth.camera.picker') }}</p>
        <p v-if="error" role="alert" class="notification is-warning" data-testid="qr-error">{{ $t(error.key, { status: error.status }) }}</p>
        <p class="help">{{ $t('fn2auth.camera.photoHelp') }}</p>
    </section>
</template>

<style scoped>
.fn2auth-file { position: relative; overflow: hidden; min-height: 44px; max-width: 100%; white-space: normal; }
.fn2auth-file input { position: absolute; inset: 0; height: 100%; width: 100%; opacity: 0; cursor: pointer; }
.fn2auth-file:focus-within { outline: 2px solid currentColor; outline-offset: 2px; }
.fn2auth-import { margin: 1rem 0; overflow-wrap: anywhere; }
</style>
