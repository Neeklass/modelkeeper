import {Fragment} from 'react'

/**
 * A file path that wraps after separators (\ and /) instead of in the
 * middle of a folder name.
 */
export function PathText({path}: { path: string }) {
    const parts = path.split(/(?<=[\/])/)
    return <>{parts.map((p, i) => <Fragment key={i}>{p}{i < parts.length - 1 && <wbr/>}</Fragment>)}</>
}
