import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import './i18n'
import App from './App'
import {ConfirmProvider} from './components/ConfirmDialog'
import {ToastProvider} from './components/Toast'

createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
        <ConfirmProvider>
            <ToastProvider>
                <App/>
            </ToastProvider>
        </ConfirmProvider>
    </React.StrictMode>
)
