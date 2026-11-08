import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {GetStatus, OpenDataFolder, SetLanguage} from '../wailsjs/go/main/App'
import {main} from '../wailsjs/go/models'
import i18n, {languages, resolveLanguage, systemLanguage} from './i18n'
import {Msg, toMsg, translateMsg} from './i18n/msg'
import {RootsPanel} from './components/RootsPanel'
import {ModelsView} from './components/ModelsView'

type Tab = 'models' | 'folders' | 'settings'

function App() {
    const {t} = useTranslation()
    const [status, setStatus] = useState<main.AppStatus | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [tab, setTab] = useState<Tab>('models')

    useEffect(() => {
        GetStatus().then(s => {
            i18n.changeLanguage(resolveLanguage(s.language))
            setStatus(s)
        }).catch(e => setError(toMsg(e)))
    }, [])

    if (!status) {
        return <div className="center">{error ? translateMsg(error) : t('app.loading')}</div>
    }

    if (status.startupError) {
        return (
            <div className="center">
                <h1>{t('error.startupTitle')}</h1>
                <p className="error">{translateMsg(status.startupError as Msg)}</p>
            </div>
        )
    }

    return (
        <div className="shell">
            <header className="row">
                <h1>{t('app.title')}</h1>
                <nav className="tabs" role="tablist">
                    {(['models', 'folders', 'settings'] as Tab[]).map(k => (
                        <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? 'active' : ''}
                                onClick={() => setTab(k)}>{t(`tabs.${k}`)}</button>
                    ))}
                </nav>
            </header>
            {tab === 'models' && <ModelsView/>}
            {tab === 'folders' && <main><RootsPanel/></main>}
            {tab === 'settings' && <main><SettingsPanel status={status} onChange={setStatus}/></main>}
        </div>
    )
}

function SettingsPanel({status, onChange}: { status: main.AppStatus; onChange: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const sysName = languages.find(l => l.code === systemLanguage())?.name ?? systemLanguage()

    async function changeLanguage(code: string) {
        try {
            await SetLanguage(code)
            await i18n.changeLanguage(resolveLanguage(code))
            onChange(main.AppStatus.createFrom({...status, language: code}))
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (
        <section className="card">
            <h2>{t('settings.title')}</h2>
            <label>
                {t('settings.language')}
                <select value={status.language} onChange={e => changeLanguage(e.target.value)}>
                    <option value="">{t('settings.languageSystem', {name: sysName})}</option>
                    {languages.map(l => <option key={l.code} value={l.code}>{l.name}</option>)}
                </select>
            </label>
            <div className="row">
                <span>{t('settings.dataFolder')}: <code>{status.dataDir}</code></span>
                <button onClick={() => OpenDataFolder().catch(e => setError(toMsg(e)))}>
                    {t('settings.openDataFolder')}
                </button>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

export default App
