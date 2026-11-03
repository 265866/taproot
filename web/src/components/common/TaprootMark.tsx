import {
  TAPROOT_MARK_FILLED_PATHS,
  TAPROOT_MARK_STROKE_PATH,
  TAPROOT_MARK_STROKE_WIDTH,
  TAPROOT_MARK_TRANSFORM,
  TAPROOT_MARK_VIEWBOX,
} from '@/assets/taprootMark';

/** The taproot glyph with no tile, drawn in `--mark-color` so it fits every theme. */
export function TaprootMark({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox={TAPROOT_MARK_VIEWBOX}
      className={className}
      aria-hidden="true"
      focusable="false"
      style={{ color: 'var(--mark-color)' }}
    >
      <g transform={TAPROOT_MARK_TRANSFORM} fill="currentColor">
        {TAPROOT_MARK_FILLED_PATHS.map((d) => (
          <path key={d} d={d} />
        ))}
        <path
          d={TAPROOT_MARK_STROKE_PATH}
          fill="none"
          stroke="currentColor"
          strokeWidth={TAPROOT_MARK_STROKE_WIDTH}
          strokeLinecap="round"
        />
      </g>
    </svg>
  );
}
