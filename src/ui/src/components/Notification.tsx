import type { Notification, NotifLevel } from '../types'
import { Icon, type IconName } from './Icon'

interface NotificationListProps {
  items: Notification[]
  onDismiss: (id: number) => void
}

/** Cap how many toasts stack up at once — older ones are collapsed into a
 *  "+N earlier" row rather than crowding the screen. */
const MAX_VISIBLE = 5

const LEVEL_ICON: Record<NotifLevel, IconName> = {
  info: 'info',
  success: 'check-circle',
  error: 'x-circle',
}

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
            <span className="notif-title">
              <Icon name={LEVEL_ICON[n.level]} size={15} />
              {n.title}
            </span>
            <button className="notif-close" aria-label="Dismiss notification" onClick={() => onDismiss(n.id)}>
              <Icon name="x" size={15} />
            </button>
          </div>
          {n.detail && <div className="notif-detail">{n.detail}</div>}
        </div>
      ))}
    </div>
  )
}
