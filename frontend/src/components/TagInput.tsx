import {useId, useState} from 'react'
import {catalog} from '../../wailsjs/go/models'

/**
 * Text input for tags with autocomplete from existing tags. Enter (or the
 * button) submits; several tags can be separated by commas.
 */
export function TagInput({allTags, placeholder, buttonLabel, disabled, onSubmit}: {
    allTags: catalog.TagCount[]
    placeholder: string
    buttonLabel: string
    disabled?: boolean
    onSubmit: (names: string[]) => Promise<void> | void
}) {
    const [value, setValue] = useState('')
    const listId = useId()
    const submit = async () => {
        const names = value.split(',').map(s => s.trim()).filter(Boolean)
        if (names.length === 0) return
        await onSubmit(names)
        setValue('')
    }
    return (
        <span className="row tag-input">
            <input list={listId} value={value} placeholder={placeholder} disabled={disabled}
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
