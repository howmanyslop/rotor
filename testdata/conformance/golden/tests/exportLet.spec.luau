-- Compiled with roblox-ts v3.0.0
local y = 0
local Foo = {}
do
	local _container = Foo
	_container.x = 1
	y = _container.x
end
local Bar = {}
do
	local _container = Bar
	local plr = {
		ClassName = "Player",
		Name = "Validark",
	}
	local a = plr.ClassName
	_container.a = a
	_container.b = plr.ClassName
	_container.c = plr.Name
	_container.ClassName = plr.ClassName
	_container.Name = plr.Name
end
return function()
	it("should allow mutatable exports", function()
		expect(Foo.x).to.equal(1)
		Foo.x = 2
		expect(Foo.x).to.equal(2)
		expect(y).to.equal(1)
	end)
	it("should allow destructured let exports", function()
		-- const check
		expect(Bar.a).to.equal("Player")
		expect(Bar.ClassName).to.equal("Player")
		Bar.ClassName = "Attacker"
		expect(Bar.ClassName).to.equal("Attacker")
		expect(Bar.Name).to.equal("Validark")
		expect(Bar.b).to.equal("Player")
		expect(Bar.c).to.equal("Validark")
		Bar.b = "Nope"
		Bar.c = "Osyris"
		expect(Bar.b).to.equal("Nope")
		expect(Bar.c).to.equal("Osyris")
	end)
end
