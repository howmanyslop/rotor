declare function useEffect(callback: () => number, dependencies: unknown[]): void;

useEffect(
	function useEffect(): number {
		return useEffect();
	},
	[],
);
