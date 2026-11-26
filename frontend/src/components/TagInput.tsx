import {useId, useState} from 'react'
import {catalog} from '../../wailsjs/go/models'

/**
 * Text input for tags with autocomplete from existing tags. Enter (or the
 * button) submits; several tags can be separated by commas.
 */
export function TagInput({allTags, placeholder, buttonLabel, disabled, autoFocus, onSubmit}: {
    allTags: catalog.TagCount[]
    placeholder: string
    buttonLabel: string
    disabled?: boolean
    autoFocus?: boolean
    onSubmit: (names: string[]) => Promise<void> | void
}) {
    const [value, setValue] = useState('')
    const listId = useId()
    // The field stays usable while a tag is saved: what is typed meanwhile is
    // kept (found in the release test: keystrokes right after Enter were lost).
    const submit = async () => {
        if (disabled) return
        const sent = value
        const names = sent.split(',').map(s => s.trim()).filter(Boolean)
        if (names.length === 0) return
        setValue('')
        try {
            await onSubmit(names)
        } catch {
            setValue(v => v || sent)
        }
    }
    return (
        <span className="row tag-input">
            <input list={listId} value={value} placeholder={placeholder} aria-busy={disabled} autoFocus={autoFocus}
                   onChange={e => setValue(e.target.value)}
                   onKeyDown={e => {
                       if (e.key === 'Enter') {
                           e.preventDefault()
                           submit()
                       }
                   }}/>
            <datalist id={listId}>
                {allTags.map(t => <option key={t.id} value={t.name}/>)}
            </datalist>
            <button onClick={submit} disabled={disabled || !value.trim()}>{buttonLabel}</button>
        </span>
    )
}
