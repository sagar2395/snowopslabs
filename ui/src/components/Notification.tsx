import type { Notification } from '../types'

interface NotificationListProps {
  items: Notification[]
  onDismiss: (id: number) => void
}

/** Cap how many toasts stack up at once — older ones are hidden, not lost,
 *  because errors require explicit dismissal anyway. */
const MAX_VISIBLE = 5

export function NotificationList({ items, onDismiss }: NotificationListProps) {
  if (items.length === 0) return null

  const visible = items.slice(-MAX_VISIBLE)
  const hidden = items.length - visible.length

  return (
    <div className="notif-container">
      {hidden > 0 && (
        <div className="notif notif-info notif-overflow">
          <span className="notif-detail">+{hidden} earlier notification{hidden > 1 ? 's' : ''}</span>
        </div>
      )}
      {visible.map(n => (
        <div
          key={n.id}
          className={`notif notif-${n.level}`}
          role={n.level === 'error' ? 'alert' : 'status'}
        >
          <div className="notif-header">
            <span className="notif-title">{n.title}</span>
            <button className="notif-close" aria-label="Dismiss notification" onClick={() => onDismiss(n.id)}>×</button>
          </div>
          {n.detail && <div className="notif-detail">{n.detail}</div>}
        </div>
      ))}
    </div>
  )
}
