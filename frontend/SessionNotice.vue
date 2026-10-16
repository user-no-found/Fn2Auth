<script setup>
import { sessionState } from './session'
const reload = () => window.location.reload()
</script>

<template>
    <aside v-if="sessionState.error" class="fn2auth-session notification is-warning" role="alert" data-testid="session-error">
        <p>{{ $t('fn2auth.session.' + sessionState.error.key, { status: sessionState.error.status }) }}</p>
        <button type="button" class="button mt-2" @click="reload" data-testid="session-reload">{{ $t('fn2auth.session.retry') }}</button>
    </aside>
</template>

<style scoped>
.fn2auth-session { position: relative; z-index: 30; margin: 1rem; overflow-wrap: anywhere; }
</style>
