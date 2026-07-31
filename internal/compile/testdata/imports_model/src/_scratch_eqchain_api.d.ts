export interface Api {
	readonly emit: {
		(value: unknown): void;
	};
}

declare const VfxForge: Api;
export = VfxForge;
