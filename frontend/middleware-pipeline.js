/** Settle every asynchronous navigation failure without a login redirect loop. */
import { presentFailure } from './session'

export default function middlewarePipeline(context, middlewares, index) {
    const middleware = middlewares[index]
    if (!middleware) return context.next
    return () => Promise.resolve().then(() => middleware({ ...context,
        nextMiddleware: middlewarePipeline(context, middlewares, index + 1),
    })).catch(error => { presentFailure(error); context.next(false) })
}
