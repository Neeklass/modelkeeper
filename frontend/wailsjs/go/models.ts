export namespace backup {
	
	export class RestoreCheck {
	    time: number;
	    snapshot: string;
	    path: string;
	    external: boolean;
	    ok: boolean;
	    problem?: string;
	    models: number;
	    tags: number;
	
	    static createFrom(source: any = {}) {
	        return new RestoreCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.snapshot = source["snapshot"];
	        this.path = source["path"];
	        this.external = source["external"];
	        this.ok = source["ok"];
	        this.problem = source["problem"];
	        this.models = source["models"];
	        this.tags = source["tags"];
	    }
	}

}

export namespace catalog {
	
	export class FileTypesChange {
	    removed: number;
	    recategorized: number;
	    archivesPending: number;
	
	    static createFrom(source: any = {}) {
	        return new FileTypesChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.removed = source["removed"];
	        this.recategorized = source["recategorized"];
	        this.archivesPending = source["archivesPending"];
	    }
	}
	export class Option {
	    id: number;
	    name: string;
	    models: number;
	
	    static createFrom(source: any = {}) {
	        return new Option(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.models = source["models"];
	    }
	}
	export class FilterOptions {
	    creators: Option[];
	    exts: Option[];
	
	    static createFrom(source: any = {}) {
	        return new FilterOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.creators = this.convertValues(source["creators"], Option);
	        this.exts = this.convertValues(source["exts"], Option);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HistoryEntry {
	    id: number;
	    at: number;
	    action: string;
	    params?: {[key: string]: any};
	    models: number;
	    undone: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.at = source["at"];
	        this.action = source["action"];
	        this.params = source["params"];
	        this.models = source["models"];
	        this.undone = source["undone"];
	    }
	}
	
	export class Issue {
	    id: number;
	    kind: string;
	    msg: msg.Msg;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Issue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.msg = this.convertValues(source["msg"], msg.Msg);
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModelAnchor {
	    rootId: number;
	    rootPath: string;
	    rootKind: string;
	    creatorLevel: boolean;
	    online: boolean;
	    rootRemoved: boolean;
	    path: string;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelAnchor(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootId = source["rootId"];
	        this.rootPath = source["rootPath"];
	        this.rootKind = source["rootKind"];
	        this.creatorLevel = source["creatorLevel"];
	        this.online = source["online"];
	        this.rootRemoved = source["rootRemoved"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	    }
	}
	export class ModelFile {
	    locationId: number;
	    rootId: number;
	    path: string;
	    archivePath: string;
	    category: string;
	    ext: string;
	    size: number;
	    present: boolean;
	    variant: string;
	    hash: string;
	    copies: number;
	    archiveStatus: string;
	    rootRemoved: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.locationId = source["locationId"];
	        this.rootId = source["rootId"];
	        this.path = source["path"];
	        this.archivePath = source["archivePath"];
	        this.category = source["category"];
	        this.ext = source["ext"];
	        this.size = source["size"];
	        this.present = source["present"];
	        this.variant = source["variant"];
	        this.hash = source["hash"];
	        this.copies = source["copies"];
	        this.archiveStatus = source["archiveStatus"];
	        this.rootRemoved = source["rootRemoved"];
	    }
	}
	export class ModelDetail {
	    id: number;
	    uid: string;
	    name: string;
	    creator: string;
	    notes: string;
	    printStatus: string;
	    supports: string;
	    supportsManual: boolean;
	    favorite: boolean;
	    creatorManual: boolean;
	    origin: string;
	    originUrl: string;
	    license: string;
	    thumb: string;
	    thumbSource: string;
	    anchors: ModelAnchor[];
	    variants: string[];
	    tags: string[];
	    tagSuggestions: string[];
	    files: ModelFile[];
	    hasCorrections: boolean;
	    inactive: boolean;
	    waiting: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.uid = source["uid"];
	        this.name = source["name"];
	        this.creator = source["creator"];
	        this.notes = source["notes"];
	        this.printStatus = source["printStatus"];
	        this.supports = source["supports"];
	        this.supportsManual = source["supportsManual"];
	        this.favorite = source["favorite"];
	        this.creatorManual = source["creatorManual"];
	        this.origin = source["origin"];
	        this.originUrl = source["originUrl"];
	        this.license = source["license"];
	        this.thumb = source["thumb"];
	        this.thumbSource = source["thumbSource"];
	        this.anchors = this.convertValues(source["anchors"], ModelAnchor);
	        this.variants = source["variants"];
	        this.tags = source["tags"];
	        this.tagSuggestions = source["tagSuggestions"];
	        this.files = this.convertValues(source["files"], ModelFile);
	        this.hasCorrections = source["hasCorrections"];
	        this.inactive = source["inactive"];
	        this.waiting = source["waiting"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ModelRow {
	    id: number;
	    name: string;
	    creator: string;
	    supports: string;
	    variants: string[];
	    files: number;
	    roots: number;
	    missing: number;
	    tagSuggest: number;
	    tags: string[];
	    thumb: string;
	    favorite: boolean;
	    inactive: boolean;
	    waiting: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.creator = source["creator"];
	        this.supports = source["supports"];
	        this.variants = source["variants"];
	        this.files = source["files"];
	        this.roots = source["roots"];
	        this.missing = source["missing"];
	        this.tagSuggest = source["tagSuggest"];
	        this.tags = source["tags"];
	        this.thumb = source["thumb"];
	        this.favorite = source["favorite"];
	        this.inactive = source["inactive"];
	        this.waiting = source["waiting"];
	    }
	}
	export class ModelPage {
	    total: number;
	    items: ModelRow[];
	
	    static createFrom(source: any = {}) {
	        return new ModelPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.items = this.convertValues(source["items"], ModelRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModelQuery {
	    text: string;
	    untagged: boolean;
	    favorites: boolean;
	    tagId: number;
	    creatorId: number;
	    rootId: number;
	    ext: string;
	    supports: string;
	    printStatus: string;
	    duplicates: boolean;
	    singleLocation: boolean;
	    offline: boolean;
	    brokenArchives: boolean;
	    extractedAndArchive: boolean;
	    missing: boolean;
	    inactive: boolean;
	    waiting: boolean;
	    orphanedBy: number;
	    sort: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.untagged = source["untagged"];
	        this.favorites = source["favorites"];
	        this.tagId = source["tagId"];
	        this.creatorId = source["creatorId"];
	        this.rootId = source["rootId"];
	        this.ext = source["ext"];
	        this.supports = source["supports"];
	        this.printStatus = source["printStatus"];
	        this.duplicates = source["duplicates"];
	        this.singleLocation = source["singleLocation"];
	        this.offline = source["offline"];
	        this.brokenArchives = source["brokenArchives"];
	        this.extractedAndArchive = source["extractedAndArchive"];
	        this.missing = source["missing"];
	        this.inactive = source["inactive"];
	        this.waiting = source["waiting"];
	        this.orphanedBy = source["orphanedBy"];
	        this.sort = source["sort"];
	    }
	}
	
	export class ModelUpdate {
	    name?: string;
	    notes?: string;
	    printStatus?: string;
	    supports?: string;
	    creator?: string;
	    origin?: string;
	    originUrl?: string;
	    license?: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelUpdate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.notes = source["notes"];
	        this.printStatus = source["printStatus"];
	        this.supports = source["supports"];
	        this.creator = source["creator"];
	        this.origin = source["origin"];
	        this.originUrl = source["originUrl"];
	        this.license = source["license"];
	    }
	}
	
	export class RelocateCheck {
	    path: string;
	    sampled: number;
	    found: number;
	    sameTime: number;
	    missing: string[];
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new RelocateCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.sampled = source["sampled"];
	        this.found = source["found"];
	        this.sameTime = source["sameTime"];
	        this.missing = source["missing"];
	        this.total = source["total"];
	    }
	}
	
	export class RemovedRoot {
	    id: number;
	    path: string;
	    kind: string;
	    creatorLevel: boolean;
	    status: string;
	    lastScanAt?: number;
	    removedAt?: number;
	    live: boolean;
	    reactivated?: boolean;
	    files: number;
	    orphaned: number;
	
	    static createFrom(source: any = {}) {
	        return new RemovedRoot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.creatorLevel = source["creatorLevel"];
	        this.status = source["status"];
	        this.lastScanAt = source["lastScanAt"];
	        this.removedAt = source["removedAt"];
	        this.live = source["live"];
	        this.reactivated = source["reactivated"];
	        this.files = source["files"];
	        this.orphaned = source["orphaned"];
	    }
	}
	export class Root {
	    id: number;
	    path: string;
	    kind: string;
	    creatorLevel: boolean;
	    status: string;
	    lastScanAt?: number;
	    removedAt?: number;
	    live: boolean;
	    scanPaused: boolean;
	    reactivated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Root(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.creatorLevel = source["creatorLevel"];
	        this.status = source["status"];
	        this.lastScanAt = source["lastScanAt"];
	        this.removedAt = source["removedAt"];
	        this.live = source["live"];
	        this.scanPaused = source["scanPaused"];
	        this.reactivated = source["reactivated"];
	    }
	}
	export class RootStats {
	    files: number;
	    bytes: number;
	    missing: number;
	    unhashed: number;
	    archives: number;
	    archivesBroken: number;
	    archivesEncrypted: number;
	    archivesPending: number;
	    archiveMembers: number;
	    duplicateFiles: number;
	
	    static createFrom(source: any = {}) {
	        return new RootStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.bytes = source["bytes"];
	        this.missing = source["missing"];
	        this.unhashed = source["unhashed"];
	        this.archives = source["archives"];
	        this.archivesBroken = source["archivesBroken"];
	        this.archivesEncrypted = source["archivesEncrypted"];
	        this.archivesPending = source["archivesPending"];
	        this.archiveMembers = source["archiveMembers"];
	        this.duplicateFiles = source["duplicateFiles"];
	    }
	}
	export class UndoResult {
	    id: number;
	    at: number;
	    action: string;
	    params?: {[key: string]: any};
	    models: number;
	    undone: boolean;
	    restored: number;
	    skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new UndoResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.at = source["at"];
	        this.action = source["action"];
	        this.params = source["params"];
	        this.models = source["models"];
	        this.undone = source["undone"];
	        this.restored = source["restored"];
	        this.skipped = source["skipped"];
	    }
	}
	
	export class TagCount {
	    id: number;
	    name: string;
	    models: number;
	
	    static createFrom(source: any = {}) {
	        return new TagCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.models = source["models"];
	    }
	}

}

export namespace hashshare {
	
	export class ExportResult {
	    files: number;
	    archives: number;
	    unhashed: number;
	
	    static createFrom(source: any = {}) {
	        return new ExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.archives = source["archives"];
	        this.unhashed = source["unhashed"];
	    }
	}

}

export namespace main {
	
	export class ConfigApply {
	    detection: boolean;
	    program: boolean;
	    machine: boolean;
	    roots: string[];
	
	    static createFrom(source: any = {}) {
	        return new ConfigApply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.detection = source["detection"];
	        this.program = source["program"];
	        this.machine = source["machine"];
	        this.roots = source["roots"];
	    }
	}
	
	export class ConfigOutcome {
	    rootsAdded: number;
	    skipped: string[];
	
	    static createFrom(source: any = {}) {
	        return new ConfigOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootsAdded = source["rootsAdded"];
	        this.skipped = source["skipped"];
	    }
	}
	
	export class ConfigRootPreview {
	    path: string;
	    kind?: string;
	    creatorLevel: boolean;
	    exists: boolean;
	    added: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConfigRootPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.creatorLevel = source["creatorLevel"];
	        this.exists = source["exists"];
	        this.added = source["added"];
	    }
	}
	
	export class ConfigPreview {
	    file: string;
	    settings: settings.Settings;
	    roots: ConfigRootPreview[];
	    exported: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.settings = this.convertValues(source["settings"], settings.Settings);
	        this.roots = this.convertValues(source["roots"], ConfigRootPreview);
	        this.exported = source["exported"];
	        this.version = source["version"];
	    }

	convertValues(a: any, classs: any, asMap: boolean = false): any {
	    if (!a) {
	        return a;
	    }
	    if (a.slice && a.map) {
	        return (a as any[]).map(elem => this.convertValues(elem, classs));
	    } else if ("object" === typeof a) {
	        if (asMap) {
	            for (const key of Object.keys(a)) {
	                a[key] = new classs(a[key]);
	            }
	            return a;
	        }
	        return new classs(a);
	    }
	    return a;
	}
	}
	
	export class BackupInfo {
	    name: string;
	    path: string;
	    reason: string;
	    time: number;
	    models: number;
	    tags: number;
	    verified: boolean;
	    bytes?: number;
	
	    static createFrom(source: any = {}) {
	        return new BackupInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.reason = source["reason"];
	        this.time = source["time"];
	        this.models = source["models"];
	        this.tags = source["tags"];
	        this.verified = source["verified"];
	        this.bytes = source["bytes"];
	    }
	}
	export class AppStatus {
	    dataDir: string;
	    language: string;
	    nasRescanHours: number;
	    startupError?: msg.Msg;
	    lastBackup?: BackupInfo;
	    backups: number;
	    backupBytes: number;
	    pendingRestore: string;
	    notice?: msg.Msg;
	    locationWarning?: msg.Msg;
	    movedFrom?: string;
	    slicerPath: string;
	    externalBackupDir: string;
	    lastRestoreTest?: backup.RestoreCheck;
	    starting: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AppStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataDir = source["dataDir"];
	        this.language = source["language"];
	        this.nasRescanHours = source["nasRescanHours"];
	        this.startupError = this.convertValues(source["startupError"], msg.Msg);
	        this.lastBackup = this.convertValues(source["lastBackup"], BackupInfo);
	        this.backups = source["backups"];
	        this.backupBytes = source["backupBytes"];
	        this.pendingRestore = source["pendingRestore"];
	        this.notice = this.convertValues(source["notice"], msg.Msg);
	        this.locationWarning = this.convertValues(source["locationWarning"], msg.Msg);
	        this.movedFrom = source["movedFrom"];
	        this.slicerPath = source["slicerPath"];
	        this.externalBackupDir = source["externalBackupDir"];
	        this.lastRestoreTest = this.convertValues(source["lastRestoreTest"], backup.RestoreCheck);
	        this.starting = source["starting"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class DetectionSettings {
	    fileTypes: settings.FileTypes;
	    partNames: string[];
	    sizeTokens: string[];
	
	    static createFrom(source: any = {}) {
	        return new DetectionSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileTypes = this.convertValues(source["fileTypes"], settings.FileTypes);
	        this.partNames = source["partNames"];
	        this.sizeTokens = source["sizeTokens"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DetectionSettingsView {
	    current: DetectionSettings;
	    defaults: DetectionSettings;
	
	    static createFrom(source: any = {}) {
	        return new DetectionSettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current = this.convertValues(source["current"], DetectionSettings);
	        this.defaults = this.convertValues(source["defaults"], DetectionSettings);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ExportResult {
	    models: number;
	    tags: number;
	    overrides: number;
	    thumbnails: number;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new ExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = source["models"];
	        this.tags = source["tags"];
	        this.overrides = source["overrides"];
	        this.thumbnails = source["thumbnails"];
	        this.path = source["path"];
	    }
	}
	
	export class ImportOutcome {
	    match: userexport.Counts;
	    tags: number;
	    overrides: number;
	    overridesSkipped: number;
	    thumbnails: number;
	    rootsAdded: number[];
	    rootsChanged: number[];
	    backup: string;
	    settingsImported: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.match = this.convertValues(source["match"], userexport.Counts);
	        this.tags = source["tags"];
	        this.overrides = source["overrides"];
	        this.overridesSkipped = source["overridesSkipped"];
	        this.thumbnails = source["thumbnails"];
	        this.rootsAdded = source["rootsAdded"];
	        this.rootsChanged = source["rootsChanged"];
	        this.backup = source["backup"];
	        this.settingsImported = source["settingsImported"];
	    }

	convertValues(a: any, classs: any, asMap: boolean = false): any {
	    if (!a) {
	        return a;
	    }
	    if (a.slice && a.map) {
	        return (a as any[]).map(elem => this.convertValues(elem, classs));
	    } else if ("object" === typeof a) {
	        if (asMap) {
	            for (const key of Object.keys(a)) {
	                a[key] = new classs(a[key]);
	            }
	            return a;
	        }
	        return new classs(a);
	    }
	    return a;
	}
	}
	
	export class ImportPreview {
	    file: string;
	    plan: userexport.Plan;
	    roots: catalog.Root[];
	
	    static createFrom(source: any = {}) {
	        return new ImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.plan = this.convertValues(source["plan"], userexport.Plan);
	        this.roots = this.convertValues(source["roots"], catalog.Root);
	    }

	convertValues(a: any, classs: any, asMap: boolean = false): any {
	    if (!a) {
	        return a;
	    }
	    if (a.slice && a.map) {
	        return (a as any[]).map(elem => this.convertValues(elem, classs));
	    } else if ("object" === typeof a) {
	        if (asMap) {
	            for (const key of Object.keys(a)) {
	                a[key] = new classs(a[key]);
	            }
	            return a;
	        }
	        return new classs(a);
	    }
	    return a;
	}
	}
	
	export class ImportResult {
	    files: number;
	    archives: number;
	    archivesReread: number;
	    prefix: string;
	    checked: number;
	    matching: number;
	    scanStarted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.archives = source["archives"];
	        this.archivesReread = source["archivesReread"];
	        this.prefix = source["prefix"];
	        this.checked = source["checked"];
	        this.matching = source["matching"];
	        this.scanStarted = source["scanStarted"];
	    }
	}

	export class RootMove {
	    rootId: number;
	    from: string;
	    to: string;
	
	    static createFrom(source: any = {}) {
	        return new RootMove(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootId = source["rootId"];
	        this.from = source["from"];
	        this.to = source["to"];
	    }
	}

}

export namespace msg {
	
	export class Msg {
	    key: string;
	    params?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new Msg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.params = source["params"];
	    }
	}

}

export namespace scanner {
	
	export class Progress {
	    rootId: number;
	    runId: number;
	    mode: string;
	    phase: string;
	    status: string;
	    current: string;
	    running: boolean;
	    startedAt: number;
	    finishedAt?: number;
	    filesSeen: number;
	    filesNew: number;
	    filesChanged: number;
	    filesMissing: number;
	    hashTotal: number;
	    hashDone: number;
	    bytesTotal: number;
	    bytesDone: number;
	    archivesTotal: number;
	    archivesDone: number;
	    archivesBroken: number;
	    archivesEncrypted: number;
	    modelsFound: number;
	    modelsCreated: number;
	    thumbsTotal: number;
	    thumbsDone: number;
	    imported: number;
	    hashChanged: number;
	    issues: number;
	    error?: msg.Msg;
	
	    static createFrom(source: any = {}) {
	        return new Progress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootId = source["rootId"];
	        this.runId = source["runId"];
	        this.mode = source["mode"];
	        this.phase = source["phase"];
	        this.status = source["status"];
	        this.current = source["current"];
	        this.running = source["running"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.filesSeen = source["filesSeen"];
	        this.filesNew = source["filesNew"];
	        this.filesChanged = source["filesChanged"];
	        this.filesMissing = source["filesMissing"];
	        this.hashTotal = source["hashTotal"];
	        this.hashDone = source["hashDone"];
	        this.bytesTotal = source["bytesTotal"];
	        this.bytesDone = source["bytesDone"];
	        this.archivesTotal = source["archivesTotal"];
	        this.archivesDone = source["archivesDone"];
	        this.archivesBroken = source["archivesBroken"];
	        this.archivesEncrypted = source["archivesEncrypted"];
	        this.modelsFound = source["modelsFound"];
	        this.modelsCreated = source["modelsCreated"];
	        this.thumbsTotal = source["thumbsTotal"];
	        this.thumbsDone = source["thumbsDone"];
	        this.imported = source["imported"];
	        this.hashChanged = source["hashChanged"];
	        this.issues = source["issues"];
	        this.error = this.convertValues(source["error"], msg.Msg);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace settings {
	
	export class FileTypes {
	    model: string[];
	    slicer: string[];
	    archive: string[];
	    supplementary: string[];
	
	    static createFrom(source: any = {}) {
	        return new FileTypes(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.slicer = source["slicer"];
	        this.archive = source["archive"];
	        this.supplementary = source["supplementary"];
	    }
	}

	export class Settings {
	    version: number;
	    language: string;
	    fileTypes: FileTypes;
	    partNames: string[];
	    sizeTokens: string[];
	    nasRescanHours: number;
	    slicerPath?: string;
	    externalBackupDir?: string;
	    dataDir?: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.language = source["language"];
	        this.fileTypes = this.convertValues(source["fileTypes"], FileTypes);
	        this.partNames = source["partNames"];
	        this.sizeTokens = source["sizeTokens"];
	        this.nasRescanHours = source["nasRescanHours"];
	        this.slicerPath = source["slicerPath"];
	        this.externalBackupDir = source["externalBackupDir"];
	        this.dataDir = source["dataDir"];
	    }

	convertValues(a: any, classs: any, asMap: boolean = false): any {
	    if (!a) {
	        return a;
	    }
	    if (a.slice && a.map) {
	        return (a as any[]).map(elem => this.convertValues(elem, classs));
	    } else if ("object" === typeof a) {
	        if (asMap) {
	            for (const key of Object.keys(a)) {
	                a[key] = new classs(a[key]);
	            }
	            return a;
	        }
	        return new classs(a);
	    }
	    return a;
	}
	}

}

export namespace thumbs {
	
	export class Candidate {
	    locationId: number;
	    kind: string;
	    label: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new Candidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.locationId = source["locationId"];
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.size = source["size"];
	    }
	}

}

export namespace userexport {
	
	export class Counts {
	    byId: number;
	    byPath: number;
	    byContent: number;
	    new: number;
	    notesKept: number;
	
	    static createFrom(source: any = {}) {
	        return new Counts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.byId = source["byId"];
	        this.byPath = source["byPath"];
	        this.byContent = source["byContent"];
	        this.new = source["new"];
	        this.notesKept = source["notesKept"];
	    }
	}
	
	export class RootChoice {
	    key: string;
	    action: string;
	    rootId?: number;
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new RootChoice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.action = source["action"];
	        this.rootId = source["rootId"];
	        this.path = source["path"];
	    }
	}
	
	export class RootPlan {
	    key: string;
	    path: string;
	    kind: string;
	    removed: boolean;
	    models: number;
	    overrides: number;
	    suggested: RootChoice;
	    exists: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RootPlan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.removed = source["removed"];
	        this.models = source["models"];
	        this.overrides = source["overrides"];
	        this.suggested = this.convertValues(source["suggested"], RootChoice);
	        this.exists = source["exists"];
	    }

	convertValues(a: any, classs: any, asMap: boolean = false): any {
	    if (!a) {
	        return a;
	    }
	    if (a.slice && a.map) {
	        return (a as any[]).map(elem => this.convertValues(elem, classs));
	    } else if ("object" === typeof a) {
	        if (asMap) {
	            for (const key of Object.keys(a)) {
	                a[key] = new classs(a[key]);
	            }
	            return a;
	        }
	        return new classs(a);
	    }
	    return a;
	}
	}
	
	export class Plan {
	    appVersion: string;
	    exportedAt: string;
	    models: number;
	    tags: number;
	    overrides: number;
	    thumbnails: number;
	    hasSettings: boolean;
	    roots: RootPlan[];
	    match: Counts;
	
	    static createFrom(source: any = {}) {
	        return new Plan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appVersion = source["appVersion"];
	        this.exportedAt = source["exportedAt"];
	        this.models = source["models"];
	        this.tags = source["tags"];
	        this.overrides = source["overrides"];
	        this.thumbnails = source["thumbnails"];
	        this.hasSettings = source["hasSettings"];
	        this.roots = this.convertValues(source["roots"], RootPlan);
	        this.match = this.convertValues(source["match"], Counts);
	    }

	convertValues(a: any, classs: any, asMap: boolean = false): any {
	    if (!a) {
	        return a;
	    }
	    if (a.slice && a.map) {
	        return (a as any[]).map(elem => this.convertValues(elem, classs));
	    } else if ("object" === typeof a) {
	        if (asMap) {
	            for (const key of Object.keys(a)) {
	                a[key] = new classs(a[key]);
	            }
	            return a;
	        }
	        return new classs(a);
	    }
	    return a;
	}
	}

}

