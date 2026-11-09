export namespace catalog {
	
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
	    thumb: string;
	    thumbSource: string;
	    anchors: ModelAnchor[];
	    variants: string[];
	    tags: string[];
	    tagSuggestions: string[];
	    files: ModelFile[];
	    hasCorrections: boolean;
	
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
	        this.thumb = source["thumb"];
	        this.thumbSource = source["thumbSource"];
	        this.anchors = this.convertValues(source["anchors"], ModelAnchor);
	        this.variants = source["variants"];
	        this.tags = source["tags"];
	        this.tagSuggestions = source["tagSuggestions"];
	        this.files = this.convertValues(source["files"], ModelFile);
	        this.hasCorrections = source["hasCorrections"];
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
	
	    static createFrom(source: any = {}) {
	        return new ModelQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.untagged = source["untagged"];
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
	    }
	}
	
	export class ModelUpdate {
	    name?: string;
	    notes?: string;
	    printStatus?: string;
	    supports?: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelUpdate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.notes = source["notes"];
	        this.printStatus = source["printStatus"];
	        this.supports = source["supports"];
	    }
	}
	
	export class Root {
	    id: number;
	    path: string;
	    kind: string;
	    creatorLevel: boolean;
	    status: string;
	    lastScanAt?: number;
	    live: boolean;
	
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
	        this.live = source["live"];
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

export namespace main {
	
	export class AppStatus {
	    dataDir: string;
	    language: string;
	    nasRescanHours: number;
	    startupError?: msg.Msg;
	
	    static createFrom(source: any = {}) {
	        return new AppStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataDir = source["dataDir"];
	        this.language = source["language"];
	        this.nasRescanHours = source["nasRescanHours"];
	        this.startupError = this.convertValues(source["startupError"], msg.Msg);
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

