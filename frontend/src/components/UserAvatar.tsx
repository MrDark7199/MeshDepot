import { Show, createSignal, createEffect } from 'solid-js'

const AVATAR_COLORS = ['#e63946', '#2a9d8f', '#e9c46a', '#f4a261', '#457b9d', '#a8dadc']

export function avatarColor(name: string): string {
  return AVATAR_COLORS[(name || '?').charCodeAt(0) % AVATAR_COLORS.length]
}

interface UserAvatarProps {
  name: string
  avatarUrl?: string | null
  size?: number
  fontSize?: number
}

export function UserAvatar(props: UserAvatarProps) {
  const sz = () => props.size ?? 42
  const fs = () => props.fontSize ?? 16
  const initials = () => (props.name || '?').split(' ').map((w: string) => w[0]).join('').toUpperCase().slice(0, 2)
  const [imgError, setImgError] = createSignal(false)
  createEffect(() => { props.avatarUrl; setImgError(false) })

  const fallback = (
    <div style={{ width: `${sz()}px`, height: `${sz()}px`, 'border-radius': '50%', background: avatarColor(props.name), display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': `${fs()}px`, 'font-weight': '700', color: '#fff', 'font-family': "'DM Mono',monospace", 'flex-shrink': '0', 'user-select': 'none' }}>
      {initials()}
    </div>
  )

  return (
    <Show when={props.avatarUrl && !imgError()} fallback={fallback}>
      <img
        src={props.avatarUrl!}
        style={{ width: `${sz()}px`, height: `${sz()}px`, 'border-radius': '50%', 'object-fit': 'cover', 'flex-shrink': '0' }}
        alt={props.name}
        onError={() => setImgError(true)}
      />
    </Show>
  )
}
