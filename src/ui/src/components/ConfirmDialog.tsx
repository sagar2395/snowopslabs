import { useEffect, useRef } from 'react'

export interface ConfirmRequest {
  title: string
  message: string
  confirmLabel?: string
  /** Style the confirm button as destructive (red). Default true. */
  danger?: boolean
  onConfirm: () => void
}

interface ConfirmDialogProps {
  request: ConfirmRequest | null
  onClose: () => void
}

/** Accessible confirmation modal for destructive actions.
 *  Esc cancels; focus moves to Cancel on open and returns on close. */
export function ConfirmDialog({ request, onClose }: ConfirmDialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  const prevFocus = useRef<Element | null>(null)

  useEffect(() => {
    if (!request) return
    prevFocus.current = document.activeElement
    cancelRef.current?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      if (prevFocus.current instanceof HTMLElement) prevFocus.current.focus()
    }
  }, [request, onClose])

  if (!request) return null

  return (
    <div
      className="modal-overlay"
      onClick={e => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className="modal-card confirm-card" role="alertdialog" aria-modal="true" aria-label={request.title}>
        <h2 className="confirm-title">{request.title}</h2>
        <p className="confirm-message">{request.message}</p>
        <div className="card-footer confirm-actions">
          <button ref={cancelRef} className="btn" onClick={onClose}>Cancel</button>
          <button
            className={`btn ${request.danger === false ? 'btn-primary' : 'btn-danger'}`}
            onClick={() => { onClose(); request.onConfirm() }}
          >
            {request.confirmLabel ?? 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  )
}
