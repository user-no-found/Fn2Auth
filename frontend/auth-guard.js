/** Bootstrap the complete upstream application with a trusted NAS identity. */
import authService from '@/services/authService'
import { publicConfig, presentFailure } from './session'

export default async function authGuard({ next, nextMiddleware, stores }) {
    const { user, appSettings } = stores
    try {
        const config = await publicConfig()
        if (!user.isAuthenticated) {
            const { data } = await authService.getCurrentUser({ returnError: true })
            await user.loginAs({ id: data.id, name: config.user.username, email: data.email,
                oauth_provider: data.oauth_provider, authenticated_by_proxy: true,
                preferences: { ...data.preferences, AutoSaveQrcodedAccount: false }, isAdmin: data.is_admin })
            for (const [key, value] of Object.entries(data.appSettings || {})) appSettings[key] = value
        }
        user.name = config.user.username
        return nextMiddleware()
    } catch (error) {
        presentFailure(error)
        return next(false)
    }
}
