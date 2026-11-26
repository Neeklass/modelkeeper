/**
 * The modelkeeper icon (a D20 whose lower half turns into print layers), the
 * same as build/appicon.svg. Its colors are fixed: the brand looks the same in
 * the dark and the light theme.
 */
export function AppIcon({size = 22}: { size?: number }) {
    return (
        <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
            <rect x="2" y="2" width="60" height="60" rx="14" fill="#1e1e1e"/>
            <path d="M32 12 L49 22 V42 L32 52 L15 42 V22 Z" fill="none" stroke="#f2f2f2" strokeWidth="3.2" strokeLinejoin="round"/>
            <path d="M32 12 L24 34 H40 Z" fill="#f2f2f2"/>
            <path d="M15 22 L24 34 M49 22 L40 34 M24 34 L32 52 L40 34" fill="none" stroke="#f2f2f2" strokeWidth="2.4"
                  strokeLinejoin="round" opacity=".75"/>
            <path d="M19 45 H45 M17 40 H24 M40 40 H47" stroke="#f2f2f2" strokeWidth="2" strokeLinecap="round" opacity=".45"/>
        </svg>
    )
}
