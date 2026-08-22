interface ErrorStateProps {
  title?: string
  message: string
  onRetry?: () => void
  retrying?: boolean
}

/** Inline error display with a retry affordance — used when a view's primary
 *  data fails to load, instead of leaving a misleading empty state. */
export function ErrorState({ title = 'Something went wrong', message, onRetry, retrying }: ErrorStateProps) {
  return (
    <div className="error-state" role="alert">
      <div className="error-state-title">⚠ {title}</div>
      <div className="error-state-message">{message}</div>
      {onRetry && (
        <button className="btn btn-sm" onClick={onRetry} disabled={retrying}>
          {retrying ? 'Retrying…' : 'Retry'}
        </button>
      )}
    </div>
  )
}
