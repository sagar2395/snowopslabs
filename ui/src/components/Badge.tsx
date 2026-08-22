interface BadgeProps {
  variant: 'running' | 'stopped' | 'pending' | 'category' | 'runtime'
  children: React.ReactNode
}

export function Badge({ variant, children }: BadgeProps) {
  return <span className={`badge badge-${variant}`}>{children}</span>
}

export function StatusBadge({ active }: { active: boolean }) {
  return <Badge variant={active ? 'running' : 'stopped'}>{active ? 'Active' : 'Inactive'}</Badge>
}

export function DeployedBadge({ deployed }: { deployed: boolean }) {
  return <Badge variant={deployed ? 'running' : 'stopped'}>{deployed ? 'Deployed' : 'Not Deployed'}</Badge>
}
