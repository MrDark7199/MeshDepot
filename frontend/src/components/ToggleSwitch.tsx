/**
 * The on/off switch used across the application.
 *
 * Lives on its own because three places want it - the notification preferences,
 * and the two delete confirmations - and a switch that looks slightly different
 * in each is the kind of drift that starts with a copy.
 */
export function ToggleSwitch(props: { checked: boolean; onChange: (newValue: boolean) => void; disabled?: boolean }) {
  return (
    // A disabled switch swallows the click rather than being merely faded: the
    // e-mail column is greyed out when mail cannot be delivered, and flipping it
    // would store a preference that can never take effect.
    <div onClick={() => { if (!props.disabled) props.onChange(!props.checked) }}
      style={{ width: '42px', height: '24px', 'border-radius': '12px', background: props.checked ? 'var(--accent)' : 'var(--bg4)', cursor: props.disabled ? 'not-allowed' : 'pointer', position: 'relative', transition: 'background 0.2s', 'flex-shrink': '0' }}>
      <div style={{ position: 'absolute', top: '3px', left: props.checked ? '21px' : '3px', width: '18px', height: '18px', 'border-radius': '50%', background: '#fff', transition: 'left 0.2s', 'box-shadow': '0 1px 3px rgba(0,0,0,0.3)' }} />
    </div>
  )
}
