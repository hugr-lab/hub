/**
 * Thin stroke path-icon (viewBox 0 0 16 16) matching the prototype's inline SVG
 * nav/action icons, plus the icon path table lifted from the design.
 */
export function PathIcon({
  d,
  size = 15,
  className,
  strokeWidth = 1.4,
}: {
  d: string
  size?: number
  className?: string
  strokeWidth?: number
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
    >
      <path d={d} />
    </svg>
  )
}

export const navIcons = {
  dashboard: 'M2.5 2.5h4.5v4.5H2.5zM9 2.5h4.5v4.5H9zM2.5 9h4.5v4.5H2.5zM9 9h4.5v4.5H9z',
  chat: 'M2.5 3h11v7.5H6.5L3.5 13v-2.5h-1z',
  agents: 'M3.5 4.5h9V11h-9zM6 11v2.5h4V11M8 2v2.5M5.8 7.7h.01M10.2 7.7h.01',
  skills: 'M8 1.5l5.5 2.8v7.4L8 14.5l-5.5-2.8V4.3zM8 1.5v6.2M2.5 4.3L8 7.7l5.5-3.4',
  ds: 'M3 3.5c0-1.8 10-1.8 10 0v9c0 1.8-10 1.8-10 0zM3 3.5c0 1.8 10 1.8 10 0M3 8c0 1.8 10 1.8 10 0',
  cat: 'M2 3h5l1.5 2H14v8H2zM2 5.5h6',
  schema: 'M3 2.5h4v3H3zM9 10.5h4v3H9zM5 5.5V12h4M5 8.5h4',
  roles: 'M8 1.5l5 2v4.5c0 3-2.3 4.8-5 6-2.7-1.2-5-3-5-6V3.5zM5.8 8l1.6 1.6 2.8-3',
  keys: 'M10.5 2.5a3 3 0 1 1-2.7 4.3L6.5 8.1v1.4H5v1.5H3.5V13H1.5v-2l4.7-4.7A3 3 0 0 1 10.5 2.5zM11 5h.01',
  me: 'M8 7.5a2.8 2.8 0 1 0 0-5.6 2.8 2.8 0 0 0 0 5.6zM2.5 14c.5-3 2.8-4 5.5-4s5 1 5.5 4',
} as const

export const themeIconPath = {
  // sun (shown in light mode → click to go dark)
  light:
    'M8 11.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM8 1v1.5M8 13.5V15M1 8h1.5M13.5 8H15M3 3l1 1M12 12l1 1M13 3l-1 1M4 12l-1 1',
  // moon (shown in dark mode → click to go light)
  dark: 'M13.5 9.5A6 6 0 0 1 6.5 2.5a6 6 0 1 0 7 7z',
}
