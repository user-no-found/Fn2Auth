<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { useTwofaccounts } from '@/stores/twofaccounts'
import QrImport from './QrImport.vue'
const router = useRouter()
const accounts = useTwofaccounts()
const importer = ref(null)
function paste(event) {
    const file = [...event.clipboardData.files].find(item => item.type.startsWith('image/'))
    if (file) { event.preventDefault(); void importer.value?.read(file) }
}
onMounted(() => document.addEventListener('paste', paste))
onBeforeUnmount(() => document.removeEventListener('paste', paste))
</script>

<template>
    <StackLayout :is-vertical-centered="true">
        <template #content>
            <div class="has-text-centered px-4">
                <p v-if="accounts.isEmpty" class="mb-4">{{ $t('message.add_first_account') }}</p>
                <RouterLink class="button is-link is-medium is-rounded" :to="{name:'capture'}">{{ $t('fn2auth.camera.start') }}</RouterLink>
                <QrImport ref="importer" />
                <div class="buttons is-centered mt-4">
                    <RouterLink class="button is-rounded" :to="{name:'createAccount'}">{{ $t('fn2auth.camera.manual') }}</RouterLink>
                    <RouterLink id="btnImport" class="button is-rounded" :to="{name:'importAccounts'}">{{ $t('label.import') }}</RouterLink>
                </div>
            </div>
        </template>
        <template #footer><VueFooter><NavigationButton v-if="!accounts.isEmpty" action="back" @goback="router.push({name:'accounts'})" /></VueFooter></template>
    </StackLayout>
</template>
