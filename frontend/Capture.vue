<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { useBusStore } from '@/stores/bus'
import QrImport from './QrImport.vue'
import { createCameraSession } from './camera-session'
import { previewCode } from './qr-workflow'
import { qrError } from './qr-errors'

const video = ref(null)
const state = ref('idle')
const error = ref(null)
const devices = ref([])
const device = ref('')
const router = useRouter()
const bus = useBusStore()
let session = null
let controller = null
let generation = 0

function stop() {
    generation++
    session?.stop()
    controller?.abort(); controller = null
    state.value = 'paused'
}
function start() {
    stop(); error.value = null
    void session?.start(device.value)
}
async function ready(next) {
    state.value = next
    if (next !== 'scanning') return
    const attempt = generation
    try {
        const list = await navigator.mediaDevices.enumerateDevices()
        if (attempt === generation) devices.value = list.filter(item => item.kind === 'videoinput' && item.deviceId)
    } catch { /* Device enumeration is optional after permission is granted. */ }
}
async function detected(value) {
    stop()
    const attempt = generation
    const active = controller = new AbortController()
    state.value = 'validating'
    try { await previewCode(value, { signal: active.signal, bus, router }) }
    catch (reason) {
        if (attempt === generation && reason.code !== 'ERR_CANCELED') error.value = qrError(reason)
    } finally {
        if (attempt === generation) { state.value = 'paused'; controller = null }
    }
}
function visibility() { if (document.hidden) stop() }
onMounted(() => {
    session = createCameraSession({ video: video.value, onState: ready, onResult: detected,
        onError: key => { state.value = 'paused'; error.value = { key: 'fn2auth.camera.' + key } } })
    document.addEventListener('visibilitychange', visibility)
    window.addEventListener('pagehide', stop)
})
onBeforeUnmount(() => {
    stop()
    document.removeEventListener('visibilitychange', visibility)
    window.removeEventListener('pagehide', stop)
})
</script>

<template>
    <StackLayout>
        <template #content>
            <section class="fn2auth-camera has-text-centered" data-testid="camera-page">
                <h1 class="title is-4">{{ $t('fn2auth.camera.title') }}</h1>
                <video ref="video" playsinline muted class="fn2auth-video" :hidden="!['playback', 'scanning'].includes(state)"></video>
                <p role="status" data-testid="camera-status" :data-state="state">{{ $t('fn2auth.camera.' + state) }}</p>
                <p v-if="error" role="alert" class="notification is-warning" data-testid="camera-error">{{ $t(error.key, { status: error.status }) }}</p>
                <div class="buttons is-centered mt-3">
                    <button v-if="['idle','paused'].includes(state)" type="button" class="button is-link" @click="start" data-testid="camera-start">{{ $t('fn2auth.camera.start') }}</button>
                    <button v-else type="button" class="button" @click="stop" data-testid="camera-stop">{{ $t('fn2auth.camera.stop') }}</button>
                </div>
                <label v-if="devices.length > 1" class="is-block mb-3">
                    {{ $t('fn2auth.camera.device') }}
                    <select v-model="device" @change="stop">
                        <option value="">{{ $t('fn2auth.camera.defaultDevice') }}</option>
                        <option v-for="item in devices" :key="item.deviceId" :value="item.deviceId">{{ item.label || $t('fn2auth.camera.defaultDevice') }}</option>
                    </select>
                </label>
                <QrImport @choosing="stop" />
                <RouterLink class="button is-rounded" :to="{ name: 'createAccount' }" data-testid="qr-manual">{{ $t('fn2auth.camera.manual') }}</RouterLink>
            </section>
        </template>
        <template #footer><VueFooter><NavigationButton action="back" @goback="router.push({name:'accounts'})" /></VueFooter></template>
    </StackLayout>
</template>

<style scoped>
.fn2auth-camera { max-width: 48rem; margin: auto; padding: 1rem; overflow-wrap: anywhere; }
.fn2auth-video { width: 100%; max-height: 50vh; object-fit: contain; border-radius: .75rem; }
.fn2auth-camera button, .fn2auth-camera select { min-height: 44px; max-width: 100%; }
</style>
