#!/usr/bin/env python3
"""Reference WSJT-X 3.0.2 datagrams, serialized by Qt rather than the Go parser."""
import pathlib
import sys

from PyQt5.QtCore import QByteArray, QDataStream, QIODevice, QTime, qVersion


def text(stream, value):
    stream << (QByteArray() if value is None else QByteArray(value.encode("utf-8")))


def status_core(stream):
    stream.writeUInt64(14074000)
    for value in ["FT8", "K1ABC", "-07", "FT8"]:
        text(stream, value)
    for value in [True, False, True]:
        stream.writeBool(value)
    stream.writeUInt32(1234)
    stream.writeUInt32(1500)
    for value in ["W1AW", "FN31", "EM00"]:
        text(stream, value)


def decode_core(stream):
    stream.writeBool(True)
    stream << QTime(23, 59, 59, 999)
    stream.writeInt32(-23)
    # QDataStream's default DoublePrecision also applies to float operators.
    stream.writeFloat(-0.25)
    stream.writeUInt32(1750)
    text(stream, "~")
    text(stream, "CQ K1ABC EM00")


def generate(directory):
    directory.mkdir(parents=True, exist_ok=True)

    def fixture(name, schema, kind, payload):
        data = QByteArray()
        stream = QDataStream(data, QIODevice.WriteOnly)
        stream.setVersion(QDataStream.Qt_5_2 if schema == 2 else QDataStream.Qt_5_4)
        stream.setByteOrder(QDataStream.BigEndian)
        assert stream.floatingPointPrecision() == QDataStream.DoublePrecision
        for value in [0xADBCCBDA, schema, kind]:
            stream.writeUInt32(value)
        text(stream, "WSJT-X - radio1")
        payload(stream)
        assert stream.status() == QDataStream.Ok
        (directory / (name + ".dat")).write_bytes(bytes(data))

    def heartbeat(stream):
        stream.writeUInt32(3)
        text(stream, "3.0.2")
        text(stream, "r-test")

    def full_decode(stream):
        decode_core(stream)
        stream.writeBool(True)
        stream.writeBool(True)

    def empty(_):
        pass

    for schema in [2, 3]:
        suffix = "-s" + str(schema)

        def full_status(stream):
            status_core(stream)
            boundaries = [stream.device().pos()]
            for method, value in [
                (stream.writeBool, False),
                (lambda v: text(stream, v), "A"),
                (stream.writeBool, True),
                (stream.writeUInt8, 7),
                (stream.writeUInt32, 50),
                (stream.writeUInt32, 15),
                (lambda v: text(stream, v), "Test α"),
                (lambda v: text(stream, v), "CQ W1AW FN31"),
            ]:
                method(value)
                boundaries.append(stream.device().pos())
            (directory / ("status-full" + suffix + ".boundaries")).write_text(
                "".join(str(n) + "\n" for n in boundaries)
            )

        fixture("heartbeat" + suffix, schema, 0, heartbeat)
        fixture("status-core" + suffix, schema, 1, status_core)
        fixture("status-full" + suffix, schema, 1, full_status)
        fixture("decode-core" + suffix, schema, 2, decode_core)
        fixture("decode-full" + suffix, schema, 2, full_decode)
        fixture("clear" + suffix, schema, 3, empty)
        fixture("close" + suffix, schema, 6, empty)

    def null_decode(stream):
        stream.writeBool(False)
        stream << QTime()
        stream.writeInt32(0)
        stream.writeFloat(0)
        stream.writeUInt32(0)
        text(stream, None)
        text(stream, "")
        stream.writeBool(False)
        stream.writeBool(False)

    def null_status(stream):
        stream.writeUInt64(0)
        for value in [None, "", "", None]:
            text(stream, value)
        for value in [False, False, False]:
            stream.writeBool(value)
        stream.writeUInt32(0)
        stream.writeUInt32(0)
        for value in [None, None, None]:
            text(stream, value)
        stream.writeBool(False)
        text(stream, None)
        stream.writeBool(False)
        stream.writeUInt8(0)
        stream.writeUInt32(0xFFFFFFFF)
        stream.writeUInt32(0xFFFFFFFF)
        text(stream, None)
        text(stream, "")

    fixture("heartbeat-legacy", 2, 0, empty)
    fixture("decode-null", 3, 2, null_decode)
    fixture("status-null", 3, 1, null_status)
    fixture("ignored", 3, 42, lambda s: s.writeUInt32(0xFFFFFFFF))
    (directory / "qt-version.txt").write_text(qVersion() + "\n")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: wsjtx-protocol-fixtures.py OUTPUT_DIRECTORY")
    generate(pathlib.Path(sys.argv[1]))
