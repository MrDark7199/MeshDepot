import { sansFont } from '../styles/formStyles'
import { For } from 'solid-js'

/** Renders star rating (0–5) with optional change handler. */
export function StarRating(props: { value: number; onChange?: (rating: number) => void }) {
  return (
    <div style={{ display: 'flex', gap: '4px' }}>
      <For each={[1,2,3,4,5]}>{star => (
        <button
          onClick={() => props.onChange?.(star === props.value ? 0 : star)}
          style={{ background: 'none', border: 'none', cursor: props.onChange ? 'pointer' : 'default', padding: '0', 'font-size': '22px', color: star <= props.value ? '#f4a261' : 'var(--border2)', 'transition': 'color 0.1s' }}>
          ★
        </button>
      )}</For>
    </div>
  )
}
