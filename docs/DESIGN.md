# Design

Which patterns this codebase uses, where, and — as importantly — which it
deliberately does not.

A pattern is worth its weight when it removes a way of being wrong. Applied for
its own sake it adds indirection and removes nothing, and Go punishes that more
than most languages: it has no inheritance to lean on, and its interfaces are
satisfied implicitly, so an abstraction nobody needs is pure cost.

## Used

### Visitor — the painters

`layout.Painter`, in `painter.go`.

The HTML emitter began as a method on `Frame`. That put an output format inside
the type that represents the result, so adding the PDF emitter meant a second
method, and a third for anyone wanting SVG. `Frame`, whose one job is to say
where things are, would have become the place all rendering lives.

It is also the wrong dependency. A frame is a *fact about geometry*; HTML is an
*opinion about how to write that fact down*. The fact should not know the
opinion.

The decisive argument is neither: the traversal is where the subtle work is —
content origins, nesting, painting order — and an emitter that re-derived it
would be a chance to derive it differently. That is precisely the class of bug
this engine exists to remove. One traversal, many painters.

In the form Go makes natural — an interface with one method per node shape —
rather than the textbook double dispatch through `Accept`, which would buy
nothing here.

### Strategy — the layout algorithms

`layout.Layouter`, in `strategy.go`.

Measuring was one function with a `switch` on the display kind, and the switch
had to be repeated: once to measure, once per emitter to draw. Adding a kind
meant finding every switch, and the compiler had nothing to say about the one
you missed — a node whose kind no case matched simply came out with no size,
which surfaces as a hole in a page a long way from its cause.

The clinching case is `row` and `row-wrap`: **one algorithm with one flag**.
As two switch cases they could only be kept in step by hand. As
`rowLayout{wrap: bool}` the sharing is the code.

### Registry — kinds by name

`layout.layouters`, a map from display kind to strategy.

The theme is data, so a composition says `"display": "row-wrap"` and something
must turn that string into behaviour. A map rather than a switch makes
registration itself data: a kind in the vocabulary with no implementation fails
at startup with its own name in the message, instead of measuring as nothing.

### Composite — the two trees

`Node` (what to draw) and `Frame` (where it went).

Not chosen so much as inherent: a page *is* a tree. Worth naming because the
two are kept deliberately separate. `Node` is the design's intent, `Frame` the
result of applying it — and the split is what lets the engine measure twice
(once to try, once with a growing child's share known) without a node ever
holding a stale position.

### Interpreter — the composition language

`theme.Element` and `theme.Composer`.

A theme states its composition as data, with three operations and only three:
`repeat`, `when`, and `{field}` binding. The composer walks that tree against a
document.

The restraint is the design. A composition language that grows keywords becomes
a programming language, and then a theme is code again with worse tools and no
debugger. Three operations cover every CV section there is; the fourth would
need a very good argument.

### Flyweight — glyph advances

`layout.face.advances`.

A CV re-measures the same few hundred characters on every keystroke of the
preview. The cache is the difference between measurement being free and being
the reason typing lags.

### Template Method — the three passes

`Engine.Layout`: inherit, measure, place.

Fixed order, and each pass exists because the one before cannot know what it
needs. Inherit first, because a node's size depends on the font it is measured
in and that may come from an ancestor. Measure bottom-up, because a parent's
size depends on its children. Place top-down, because a child's position depends
on where its parent ended up. Folding any two together means guessing one.

## Deliberately not used

### Inheritance hierarchies for nodes

An `AbstractNode` with `TextNode`, `BoxNode`, `ImageNode` subclasses is the
reflex in an OO language. Here every node carries the same flat `Style`, and the
kind selects which members are read.

That is on purpose, and it is the same choice `fields.Field` makes. The
vocabulary is **closed**: each property has exactly one layout meaning, one CSS
declaration and one PDF operator. A flat struct makes that checkable at a
glance, and makes a property set on the wrong kind *ignored* rather than
*misread*. A hierarchy would scatter the vocabulary across types and let a
subclass quietly add a property one emitter knows about and the other does not —
exactly the divergence this engine exists to remove.

### Abstract factories, DI containers

Dependencies are passed to constructors and defaulted. `Engine` takes `Fonts`,
`Composer` takes a `Theme`. A container to wire four objects would be more code
than the wiring.

### Getters and setters over every field

Go has no property syntax, so an accessor over a public field is noise. Methods
exist on `Style` where there is a *rule* worth stating once — `LineHeightOr`
("zero means 1.2"), `WeightOf` ("bold is a heavier face of the same family, and
a style already bold stays put"), `AlignOf` ("a child's own choice wins"). Every
caller that re-derived those would be a chance to derive them differently. A
`GetWidth()` returning `Width` would not.

### Observers, events

Nothing here is asynchronous, and nothing needs to know when something else
changes. A layout is a pure function of a document and a theme.

## The invariant behind all of it

> The layout is computed once, and both renderings are drawn from the result.

Every pattern above is chosen for whether it protects that. `Painter` protects
it by giving the emitters no geometry to compute. `Layouter` protects it by
giving each kind one implementation both emitters read. The closed vocabulary
protects it by making a feature that exists on one side only impossible to
express.

A pattern that does not protect it is not in this codebase.
