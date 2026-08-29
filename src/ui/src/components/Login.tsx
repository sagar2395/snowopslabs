import { useState, useRef, useEffect } from 'react'
import type { AuthStatus } from '../types'
import { api } from '../api/client'
import { Icon } from './Icon'

interface LoginProps {
  onLoggedIn: (status: AuthStatus) => void
}

/** Sign-in form shown when auth is enabled and the user isn't authenticated.
 *  Submits credentials to /api/auth/login; the server sets an HttpOnly cookie
 *  on success, so we just lift the returned status up via onLoggedIn. */
export function Login({ onLoggedIn }: LoginProps) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const userRef = useRef<HTMLInputElement>(null)

  useEffect(() => { userRef.current?.focus() }, [])

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const status = await api.login(username, password)
      onLoggedIn(status)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <div className="login-shell">
      <form className="card login-card" onSubmit={onSubmit} aria-label="Sign in">
        <div className="login-brand">
          <span className="brand-mark" aria-hidden="true"><Icon name="snowflake" size={18} /></span>
          <h1 className="login-title">Sign in to <span className="accent">SnowOps Labs</span></h1>
        </div>

        {error && (
          <div className="error-state" role="alert">
            <div className="error-state-message">{error}</div>
          </div>
        )}

        <div className="login-field">
          <label className="login-label" htmlFor="login-username">Username</label>
          <input
            id="login-username"
            ref={userRef}
            className="search-input login-input"
            type="text"
            autoComplete="username"
            value={username}
            disabled={busy}
            onChange={e => setUsername(e.target.value)}
          />
        </div>

        <div className="login-field">
          <label className="login-label" htmlFor="login-password">Password</label>
          <input
            id="login-password"
            className="search-input login-input"
            type="password"
            autoComplete="current-password"
            value={password}
            disabled={busy}
            onChange={e => setPassword(e.target.value)}
          />
        </div>

        <div className="card-footer">
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </div>
      </form>
    </div>
  )
}
